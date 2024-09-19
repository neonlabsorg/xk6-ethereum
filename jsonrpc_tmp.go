package ethereum

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ybbus/jsonrpc/v3"
)

// ClientTmp is a jsonrpc client, we use it temporarily until the bug NDEV-3224 is fixed

type ClientTmp struct {
	client jsonrpc.RPCClient
	ctx    context.Context
}

func (c *ClientTmp) GasPrice() (uint64, error) {
	g, err := c.client.Call(c.ctx, "eth_gasPrice")
	if err != nil {
		return 0, fmt.Errorf("failed to get gasPrice: %w", err)
	}

	return parseResponse("eth_gasPrice", g)
}

func (c *ClientTmp) BlockNumber() (uint64, error) {
	g, err := c.client.Call(c.ctx, "eth_blockNumber")
	if err != nil {
		return 0, fmt.Errorf("failed to get blockNumber: %w", err)
	}

	return parseResponse("eth_blockNumber", g)
}

func parseResponse(methodName string, response *jsonrpc.RPCResponse) (uint64, error) {
	value, ok := response.Result.(string)
	if !ok {
		return 0, fmt.Errorf("failed to parse hex value to uint64 %s", methodName)
	}

	result, err := strconv.ParseUint(strings.Trim(value, "0x"), 16, 64)
	if err != nil {
		fmt.Printf("failed to convert string value into uint64: %s", methodName)
		return 0, fmt.Errorf("error message: %w", err)
	}

	return result, nil
}
