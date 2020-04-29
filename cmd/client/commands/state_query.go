// Copyright © 2017 ZhongAn Technology
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/urfave/cli.v1"

	"github.com/dappledger/AnnChain/cmd/client/commons"
	"github.com/dappledger/AnnChain/eth/common"
	"github.com/dappledger/AnnChain/eth/core/types"
	"github.com/dappledger/AnnChain/eth/rlp"
	gcmn "github.com/dappledger/AnnChain/gemmill/modules/go-common"
	cl "github.com/dappledger/AnnChain/gemmill/rpc/client"
	gtypes "github.com/dappledger/AnnChain/gemmill/types"
)

var (
	QueryCommands = cli.Command{
		Name:     "query",
		Usage:    "operations for query state",
		Category: "Query",
		Subcommands: []cli.Command{
			{
				Name:   "nonce",
				Usage:  "query account's nonce",
				Action: queryNonce,
				Flags: []cli.Flag{
					anntoolFlags.addr,
				},
			},
			{
				Name:   "pending_nonce",
				Usage:  "query account's pending nonce",
				Action: queryPendingNonce,
				Flags: []cli.Flag{
					anntoolFlags.addr,
				},
			},
			{
				Name:   "receipt",
				Usage:  "",
				Action: queryReceipt,
				Flags: []cli.Flag{
					anntoolFlags.hash,
				},
			},
			{
				Name:   "key",
				Usage:  "",
				Action: queryKey,
				Flags: []cli.Flag{
					anntoolFlags.key,
				},
			},
			{
				Name:   "key_update_history",
				Usage:  "",
				Action: queryKeyUpdateHistory,
				Flags: []cli.Flag{
					anntoolFlags.key,
					anntoolFlags.pageNum,
					anntoolFlags.pageSize,
				},
			},
		},
	}
)

func queryNonce(ctx *cli.Context) error {
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)
	rpcResult := new(gtypes.ResultQuery)

	addrHex := gcmn.SanitizeHex(ctx.String("address"))
	addr := common.Hex2Bytes(addrHex)
	query := append([]byte{1}, addr...)

	_, err := clientJSON.Call("query", []interface{}{query}, rpcResult)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	nonce := new(uint64)
	err = rlp.DecodeBytes(rpcResult.Result.Data, nonce)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	fmt.Println("query result:", *nonce)
	return nil
}

func queryPendingNonce(ctx *cli.Context) error {
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)
	rpcResult := new(gtypes.ResultQuery)

	addrHex := gcmn.SanitizeHex(ctx.String("address"))
	addr := common.Hex2Bytes(addrHex)
	query := append([]byte{13}, addr...)

	_, err := clientJSON.Call("query", []interface{}{query}, rpcResult)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	nonce := new(uint64)
	err = rlp.DecodeBytes(rpcResult.Result.Data, nonce)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	fmt.Println("query result:", *nonce)
	return nil
}

func queryReceipt(ctx *cli.Context) error {
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)
	rpcResult := new(gtypes.ResultQuery)
	hash := ctx.String("hash")
	if strings.Index(hash, "0x") == 0 {
		hash = hash[2:]
	}

	hashBytes := common.Hex2Bytes(hash)
	query := append([]byte{3}, hashBytes...)
	_, err := clientJSON.Call("query", []interface{}{query}, rpcResult)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	receiptForStorage := new(types.ReceiptForStorage)
	err = rlp.DecodeBytes(rpcResult.Result.Data, receiptForStorage)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	rt, etx, err := getTxByHash(hashBytes)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	ethSigner := &types.HomesteadSigner{}
	from, err := types.Sender(ethSigner, etx)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	response := map[string]interface{}{
		"from":             from.Hex(),
		"to":               etx.To(),
		"blockHash":        fmt.Sprintf("0x%x", rt.BlockHash),
		"blockNumber":      rt.BlockHeight,
		"status":           fmt.Sprintf("0x%x", receiptForStorage.Status),
		"transactionIndex": fmt.Sprintf("0x%x", rt.TransactionIndex),

		"PostState":         common.Bytes2Hex(receiptForStorage.PostState),
		"CumulativeGasUsed": receiptForStorage.CumulativeGasUsed,
		"Bloom":             receiptForStorage.Bloom,
		"Logs":              receiptForStorage.Logs,
		"TxHash":            receiptForStorage.TxHash.Hex(),
		"ContractAddress":   receiptForStorage.ContractAddress,
		"GasUsed":           receiptForStorage.GasUsed,
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	fmt.Println("query result:", string(responseJSON))

	return nil
}

func getTxByHash(hash []byte) (rt *gtypes.ResultTransaction, ethtx *types.Transaction, err error) {
	res := new(gtypes.ResultQuery)
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)

	_, err = clientJSON.Call("transaction", []interface{}{hash}, res)
	if err != nil {
		return
	}

	data := res.Result.Data
	rt = &gtypes.ResultTransaction{}
	err = rlp.DecodeBytes(data, rt)
	if err != nil {
		return
	}

	ethtx = &types.Transaction{}
	err = rlp.DecodeBytes(rt.RawTransaction, ethtx)
	if err != nil {
		return
	}

	return
}

func queryKey(ctx *cli.Context) error {
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)
	rpcResult := new(gtypes.ResultQuery)

	keyStr := ctx.String("key")
	query := append([]byte{11}, []byte(keyStr)...)

	_, err := clientJSON.Call("query", []interface{}{query}, rpcResult)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}
	fmt.Println("query result:", string(rpcResult.Result.Data))
	return nil
}

func queryKeyUpdateHistory(ctx *cli.Context) error {
	clientJSON := cl.NewClientJSONRPC(commons.QueryServer)
	rpcResult := new(gtypes.ResultQuery)
	//ValueHistoryResult
	keyStr := ctx.String("key")
	pageNum := ctx.Uint("page_num")
	if pageNum == 0 {
		pageNum = 1
	}
	pageSize := ctx.Uint("page_size")
	if pageSize == 0 {
		pageSize = 10
	}
	query := append([]byte{14}, putUint32(uint32(pageNum))... )
	query = append(query, putUint32(uint32(pageSize))...)
	query = append(query,[]byte(keyStr)... )

	_, err := clientJSON.Call("query", []interface{}{query}, rpcResult)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	response := &gtypes.ValueHistoryResult{}
	err = rlp.DecodeBytes(rpcResult.Result.Data, response)
	if err != nil {
		fmt.Println(rpcResult.Result)
		return cli.NewExitError(err.Error(), 127)
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return cli.NewExitError(err.Error(), 127)
	}

	fmt.Println("query result:", string(responseJSON))

	return nil
}

func putUint32(i uint32) []byte {
	index := make([]byte, 4)
	binary.BigEndian.PutUint32(index, i)
	return index
}
