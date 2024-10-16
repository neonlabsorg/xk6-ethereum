package ethereum

import (
	"fmt"
	"math/big"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/contract"
)

// Contract exposes a contract
type Contract struct {
	Contract      *contract.Contract
	Client        *Client
	SignerAddress string
}

type TxnOpts struct {
	Value    uint64
	GasPrice uint64
	GasLimit uint64
	Nonce    uint64
}

// Call executes a call on the contract
func (c *Contract) Call(method string, args ...interface{}) (map[string]interface{}, error) {
	return c.Contract.Call(method, ethgo.Latest, args...)
}

// Txn executes a transactions on the contract and waits for it to be mined
// TODO maybe use promise
func (c *Contract) Txn(method string, opts TxnOpts, args ...interface{}) (string, error) {
	txn, err := c.Contract.Txn(method, args...)
	if err != nil {
		return "", fmt.Errorf("failed to create contract transaction: %w", err)
	}

	gasPrice, err := c.Client.GasPrice()
	if err != nil {
		return "", fmt.Errorf("failed to get gas price: %w", err)
	}
	blockNumber, err := c.Client.BlockNumber()
	if err != nil {
		return "", fmt.Errorf("failed to get block number: %w", err)
	}
	block, err := c.Client.GetBlockByNumber(ethgo.BlockNumber(blockNumber), false)
	if err != nil {
		return "", fmt.Errorf("failed to get block: %w", err)
	}

	txo := contract.TxnOpts{
		Value:    big.NewInt(int64(opts.Value)),
		GasPrice: gasPrice,
		GasLimit: block.GasLimit,
		Nonce:    opts.Nonce,
	}
	txn.WithOpts(&txo)

	err = txn.Do()
	if err != nil {
		return "", fmt.Errorf("failed to send contract transaction: %w, Tx: %+v", err, txo)
	}

	return txn.Hash().String(), nil
}

func (c *Contract) GetAddress() string {
	return c.SignerAddress
}

func (c *Contract) FillInput(abiString string, method string) []byte {
	contractABI, err := abi.NewABI(abiString)
	if err != nil {
		fmt.Printf("failed to parse abi: %s", err)
		return nil
	}
	methodContract := contractABI.GetMethod(method)

	return methodContract.ID()
}
