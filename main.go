package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/ExocoreNetwork/exocore/app"
	"github.com/evmos/evmos/v14/encoding"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"cosmossdk.io/simapp/params"
	//	"github.com/cosmos/cosmos-sdk/codec"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"

	authTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	chainID = "exocoretestnet_233-1"
	homeDir = "/Users/linqing/.tmp-exocored"
	appName = "exocore"
)

var encCfg params.EncodingConfig
var txCfg client.TxConfig
var kr keyring.Keyring
var gasPrice uint64
var blockMaxGas uint64

func init() {
	config := sdk.GetConfig()
	cmdcfg.SetBech32Prefixes(config)

	encCfg = encoding.MakeConfig(app.ModuleBasics)
	txCfg = encCfg.TxConfig

	var err error
	if kr, err = keyring.New(appName, keyring.BackendTest, homeDir, nil, encCfg.Codec); err != nil {
		panic(err)
	}

	gasPrice = uint64(7)
	blockMaxGas = 10000000
}

func main() {
	cc := createGrpcConn()
	defer cc.Close()
	sendTx(cc)

}

func createGrpcConn() *grpc.ClientConn {
	grpcConn, err := grpc.Dial(
		"127.0.0.1:9090",
		grpc.WithTransportCredentials(insecure.NewCredentials()), // The Cosmos SDK doesn't support any transport security mechanism.
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(encCfg.InterfaceRegistry).GRPCCodec())),
	)
	if err != nil {
		panic(err)
	}

	return grpcConn
}

func simulateTx(cc *grpc.ClientConn, txBytes []byte) (uint64, error) {
	// Simulate the tx via gRPC. We create a new client for the Protobuf Tx service
	txClient := sdktx.NewServiceClient(cc)

	// call the Simulate method on this client.
	grpcRes, err := txClient.Simulate(
		context.Background(),
		&sdktx.SimulateRequest{
			TxBytes: txBytes,
		},
	)
	if err != nil {
		fmt.Println("debug-simulateTx-err:", err)
		return 0, err
	}

	fmt.Println("debug-simulateTx-gasinfo:", grpcRes.GasInfo) // Prints estimated gas used.

	return grpcRes.GasInfo.GasUsed, nil
}

func signMsg(cc *grpc.ClientConn, name string, msgs ...sdk.Msg) authsigning.Tx {
	txBuilder := txCfg.NewTxBuilder()
	_ = txBuilder.SetMsgs(msgs...)
	txBuilder.SetGasLimit(blockMaxGas)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", math.MaxInt64)})

	info, _ := kr.Key(name)
	fromAddr, _ := info.GetAddress()

	number, sequence, err := queryAccount(cc, fromAddr)
	if err != nil {
		fmt.Println("debug-queryAccount-err:", err)
		panic(err)
	}

	txf := tx.Factory{}.
		WithChainID(chainID).
		WithKeybase(kr).
		WithTxConfig(txCfg).
		WithAccountNumber(number).
		WithSequence(sequence)

	if err = tx.Sign(txf, "dev1", txBuilder, true); err != nil {
		panic(err)
	}

	//simulate and sign again
	fmt.Println("sign again with simulated gas")
	signedTx := txBuilder.GetTx()
	txBytes, _ := txCfg.TxEncoder()(signedTx)
	gasLimit, _ := simulateTx(cc, txBytes)
	fee := gasLimit * gasPrice
	if fee > math.MaxInt64 {
		panic("fee exceeds maxInt64")
	}
	txBuilder.SetGasLimit(gasLimit)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", int64(fee))})
	//sign agin with simulated gas used
	if err = tx.Sign(txf, "dev1", txBuilder, true); err != nil {
		panic(err)
	}

	return txBuilder.GetTx()
}

func verifySignature(cc *grpc.ClientConn, signerName string, signedTx authsigning.Tx, signatureBytes []byte) bool {
	info, _ := kr.Key(signerName)
	fromAddr, _ := info.GetAddress()
	pub, _ := info.GetPubKey()

	number, _, err := queryAccount(cc, fromAddr)
	if err != nil {
		panic(err)
	}

	bytesToSign, err := txCfg.SignModeHandler().GetSignBytes(
		txCfg.SignModeHandler().DefaultMode(),
		authsigning.SignerData{
			ChainID:       chainID,
			AccountNumber: number,
			//		Sequence:      sequence,
			PubKey:  pub,
			Address: sdk.AccAddress(pub.Address()).String(),
		},
		signedTx,
	)

	if err != nil {
		fmt.Println("debug-getBytesToSign-err:", err)
	}

	verified := pub.VerifySignature(bytesToSign, signatureBytes)

	return verified
}

func sendTx(cc *grpc.ClientConn) {
	info, _ := kr.Key("dev1")
	fromAddr, _ := info.GetAddress()

	info2, _ := kr.Key("dev2")
	toAddr, _ := info2.GetAddress()

	msg := banktypes.NewMsgSend(fromAddr, toAddr, types.NewCoins(types.NewInt64Coin("aexo", 10000)))

	fmt.Println("debug:signMsg-----")
	signedTx := signMsg(cc, "dev1", msg)

	signatures, err := signedTx.GetSignaturesV2()
	if err != nil {
		panic(err)
	}

	sigSingle, isSingle := signatures[0].Data.(*signing.SingleSignatureData)
	if !isSingle {
		panic("extract signature fail")
	}
	signatureBytes := sigSingle.Signature

	fmt.Println("debug:verifySignature-----")
	verified := verifySignature(cc, "dev1", signedTx, signatureBytes)
	fmt.Println("debug-VerifySignature:", verified)

	//-----broadcast
	fmt.Println("check balance before send")
	balance, _ := queryBalance(cc, fromAddr)
	fmt.Println("debug-balance:", balance)

	txBytes, err := txCfg.TxEncoder()(signedTx)
	if err != nil {
		panic(err)
	}

	ccRes := broadcastTxBytes(cc, txBytes)
	_ = ccRes

	//	fmt.Println("debug-braodResponse:", ccRes.TxResponse)
	time.Sleep(5 * time.Second)

	fmt.Println("check balance after send")
	balance, _ = queryBalance(cc, fromAddr)
	fmt.Println("debug-balacne:", balance)
}

func broadcastTxBytes(cc *grpc.ClientConn, txBytes []byte) *sdktx.BroadcastTxResponse {
	txClient := sdktx.NewServiceClient(cc)
	ccRes, err := txClient.BroadcastTx(
		context.Background(),
		&sdktx.BroadcastTxRequest{
			Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		},
	)
	if err != nil {
		panic(err)
	}
	return ccRes
}

func queryBalance(grpcConn *grpc.ClientConn, myAddress sdk.AccAddress) (balance *types.Coin, err error) {
	// This creates a gRPC client to query the x/bank service.
	bankClient := banktypes.NewQueryClient(grpcConn)
	bankRes, err := bankClient.Balance(
		context.Background(),
		&banktypes.QueryBalanceRequest{Address: myAddress.String(), Denom: "aexo"},
	)
	if err != nil {
		fmt.Println("debug-queryBalance:", err)
		return
	}
	balance = bankRes.GetBalance()
	return
}

func queryAccount(grpcConn *grpc.ClientConn, myAddress sdk.AccAddress) (number, sequence uint64, err error) {
	fmt.Println("debug-queryAccount-myAddress:", myAddress.String())
	authClient := authTypes.NewQueryClient(grpcConn)
	var accountRes *authTypes.QueryAccountResponse
	accountRes, err = authClient.Account(context.Background(), &authTypes.QueryAccountRequest{
		Address: myAddress.String(),
	})
	if err != nil {
		fmt.Println("debug-queryAccount-err:", err)
		return
	}
	var account authTypes.AccountI
	encCfg.Codec.UnpackAny(accountRes.Account, &account)
	number = account.GetAccountNumber()
	sequence = account.GetSequence()
	fmt.Println("debug-queryAccount-number/sequence:", number, sequence)

	fmt.Println("===")

	//AccountInfo has to be used with cosmossdk version >= 0.47.5
	accountResponse, err := authClient.AccountInfo(context.Background(), &authTypes.QueryAccountInfoRequest{
		Address: myAddress.String(),
	})
	if err != nil {
		return
	}
	number = accountResponse.Info.AccountNumber
	sequence = accountResponse.Info.Sequence
	fmt.Println("debug-queryAccountInfo-addressNumberSequence:", number, sequence)
	return
}
