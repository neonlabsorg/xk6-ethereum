// xk6 build --with github.com/distribworks/xk6-ethereum=.
package ethereum

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"bytes"
	"math/big"
	"strconv"
	"sync"
	"time"
	"net/http"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	contract "github.com/umbracle/ethgo/contract"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/umbracle/ethgo/wallet"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/metrics"
)

type Transaction struct {
	From      string
	To        string
	Input     []byte
	GasPrice  uint64
	GasFeeCap uint64
	GasTipCap uint64
	Gas       uint64
	Value     int64
	Nonce     uint64
	// eip-2930 values
	ChainId int64
}

type Client struct {
	tracer    *Tracer
	w         *wallet.Key
	client    *jsonrpc.Client
	clientTmp *ClientTmp
	chainID   *big.Int
	vu        modules.VU
	metrics   ethMetrics
	opts      *options
}

func (c *Client) Exports() modules.Exports {
	return modules.Exports{}
}

func (c *Client) Call(method string, params ...interface{}) (interface{}, error) {
	t := time.Now()
	var out interface{}
	err := c.client.Call(method, &out, params...)
	if err != nil {
		c.reportErrorMetrics(method)
		return out, err
	}
	c.reportMetricsFromStats(method, time.Since(t))
	return out, err
}

func (c *Client) GasPrice() (uint64, error) {
	t := time.Now()
	// g, err := c.client.Eth().GasPrice()
	g, err := c.clientTmp.GasPrice()
	if err != nil {
		c.reportErrorMetrics("gasPrice")
		return g, err
	}
	c.reportMetricsFromStats("gasPrice", time.Since(t))
	return g, err
}

func (c *Client) GetBalance(address string) (uint64, error) {
	t := time.Now()
	blockNumber, err := c.clientTmp.BlockNumber()
	if err != nil {
		c.reportErrorMetrics("gasPrice")
		return 0, err
	}
	c.reportMetricsFromStats("getBlockNumber", time.Since(t))

	tb := time.Now()
	b, err := c.client.Eth().GetBalance(ethgo.HexToAddress(address), ethgo.BlockNumber(blockNumber-3))
	if err != nil {
		c.reportErrorMetrics("getBalance")
		return 0, err
	}
	c.reportMetricsFromStats("getBalance", time.Since(tb))

	return b.Uint64(), err
}

// BlockNumber returns the current block number.
func (c *Client) BlockNumber() (uint64, error) {
	t := time.Now()
	// return c.client.Eth().BlockNumber()
	blockNumber, err := c.clientTmp.BlockNumber()
	if err != nil {
		c.reportErrorMetrics("getBlockNumber")
		return 0, fmt.Errorf("failed to get block number: %e", err)
	}
	c.reportMetricsFromStats("getBlockNumber", time.Since(t))
	return blockNumber, nil
}

// GetBlockByNumber returns the block with the given block number.
func (c *Client) GetBlockByNumber(number ethgo.BlockNumber, full bool) (*ethgo.Block, error) {
	t := time.Now()
	block, err := c.client.Eth().GetBlockByNumber(number, full)
	if err != nil {
		c.reportErrorMetrics("getBlockByNumber")
		return nil, fmt.Errorf("failed to get block: %e", err)
	}
	c.reportMetricsFromStats("getBlockByNumber", time.Since(t))
	return block, nil
}

// GetNonce returns the nonce for the given address.
func (c *Client) GetNonce(address string) (uint64, error) {
	t := time.Now()
	nonce, err := c.client.Eth().GetNonce(ethgo.HexToAddress(address), ethgo.Pending)
	if err != nil {
		c.reportErrorMetrics("getTransactionCount")
		return 0, fmt.Errorf("failed to get nonce: %e", err)
	}
	c.reportMetricsFromStats("getTransactionCount", time.Since(t))
	return nonce, nil
}

// EstimateGas returns the estimated gas for the given transaction.
func (c *Client) EstimateGas(tx Transaction) (uint64, error) {
	to := ethgo.HexToAddress(tx.To)

	msg := &ethgo.CallMsg{
		From:     ethgo.HexToAddress(tx.From),
		To:       &to,
		Value:    big.NewInt(tx.Value),
		Data:     []byte(tx.Input),
		GasPrice: tx.GasPrice,
		Gas:      big.NewInt(int64(tx.Gas)),
	}

	t := time.Now()
	gas, err := c.client.Eth().EstimateGas(msg)
	if err != nil {
		c.reportErrorMetrics("estimateGas")
		return 0, fmt.Errorf("failed to estimate gas: %e", err)
	}
	c.reportMetricsFromStats("estimateGas", time.Since(t))
	return gas, nil
}

// SendTransaction sends a transaction to the network.
func (c *Client) SendTransaction(tx Transaction) (string, error) {
	to := ethgo.HexToAddress(tx.To)

	if tx.Gas == 0 {
		tx.Gas = 21000
	}

	if tx.GasPrice == 0 && tx.GasFeeCap == 0 && tx.GasTipCap == 0 {
		tx.GasPrice = 5242880
	}

	t := &ethgo.Transaction{
		Type:     ethgo.TransactionLegacy,
		From:     ethgo.HexToAddress(tx.From),
		To:       &to,
		Value:    big.NewInt(tx.Value),
		Gas:      tx.Gas,
		GasPrice: tx.GasPrice,
	}

	if tx.GasFeeCap > 0 || tx.GasTipCap > 0 {
		t.Type = ethgo.TransactionDynamicFee
		t.GasPrice = 0
		t.MaxFeePerGas = big.NewInt(0).SetUint64(tx.GasFeeCap)
		t.MaxPriorityFeePerGas = big.NewInt(0).SetUint64(tx.GasTipCap)
	}

	h, err := c.client.Eth().SendTransaction(t)
	return h.String(), err
}

// SendRawTransaction signs and sends transaction to the network.
func (c *Client) SendRawTransaction(tx Transaction) (string, error) {
	to := ethgo.HexToAddress(tx.To)

	gasPrice, err := c.GasPrice()
	if err != nil {
		return "", fmt.Errorf("failed to get gas price: %e", err)
	}

	gas, err := c.EstimateGas(tx)
	if err != nil {
		return "", fmt.Errorf("failed to estimate gas: %e", err)
	}

	nonce, err := c.GetNonce(tx.From)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %e", err)
	}

	t := &ethgo.Transaction{
		Type:     ethgo.TransactionLegacy,
		From:     ethgo.HexToAddress(tx.From),
		To:       &to,
		Value:    big.NewInt(tx.Value),
		Gas:      gas,
		GasPrice: gasPrice,
		Nonce:    nonce,
		Input:    []byte(tx.Input),
		ChainID:  c.chainID,
	}

	if tx.GasFeeCap > 0 || tx.GasTipCap > 0 {
		if tx.GasPrice < tx.GasFeeCap {
			return "", fmt.Errorf("gas price is less than gas fee cap")
		}
		t.Type = ethgo.TransactionDynamicFee
		t.MaxFeePerGas = big.NewInt(0).SetUint64(t.GasPrice)
		t.MaxPriorityFeePerGas = big.NewInt(0).SetUint64(t.GasPrice - tx.GasFeeCap)
		t.GasPrice = 0
	}

	s := wallet.NewEIP155Signer(t.ChainID.Uint64())
	st, err := s.SignTx(t, c.w)
	if err != nil {
		return "", err
	}

	trlp, err := st.MarshalRLPTo(nil)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tx: %e", err)
	}

	timeNow := time.Now()
	h, err := c.client.Eth().SendRawTransaction(trlp)
	res, e := json.Marshal(t)
	if e != nil {
		return h.String(), fmt.Errorf("failed to marshal tx: %e", err)
	}
	if err != nil {
		c.reportErrorMetrics("sendRawTransaction")
		return h.String(), fmt.Errorf("failed to send tx: %v, error:  %e", string(res), err)
	}
	c.reportMetricsFromStats("sendRawTransaction", time.Since(timeNow))

	return h.String(), nil
}

// GetTransactionReceipt returns the transaction receipt for the given transaction hash.
func (c *Client) GetTransactionReceipt(hash string) (*ethgo.Receipt, error) {
	t := time.Now()
	r, err := c.client.Eth().GetTransactionReceipt(ethgo.HexToHash(hash))
	if err != nil {
		return nil, err
	}

	if r != nil {
		c.reportMetricsFromStats("getTransactionReceipt", time.Since(t))
		return r, nil
	}

	return nil, fmt.Errorf("not found")
}

// WaitForTransactionReceipt waits for the transaction receipt for the given transaction hash.
func (c *Client) WaitForTransactionReceipt(hash string, maxAttempts int) *ethgo.Receipt {
	var res *ethgo.Receipt
	var err error

	Retry(
		func() error {
			res, err = c.GetTransactionReceipt(hash)
			return err
		},
		maxAttempts, 500*time.Millisecond,
	)

	if err != nil {
		c.reportErrorMetrics("getTransactionReceipt")
		return nil
	}
	return res
}

// Accounts returns a list of addresses owned by client. This endpoint is not enabled in infrastructure providers.
func (c *Client) Accounts() ([]string, error) {
	accounts, err := c.client.Eth().Accounts()
	if err != nil {
		return nil, err
	}

	addresses := make([]string, len(accounts))
	for i, a := range accounts {
		addresses[i] = a.String()
	}

	return addresses, nil
}

// NewContract creates a new contract instance with the given ABI.
func (c *Client) NewContract(address string, abistr string, signerKey string) (*Contract, error) {
	contractABI, err := abi.NewABI(abistr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse abi: %w", err)
	}

	wallet, err := wallet.NewWalletFromPrivKey([]byte(signerKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet from private key: %w", err)
	}

	opts := []contract.ContractOption{
		contract.WithJsonRPC(c.client.Eth()),
		contract.WithSender(wallet),
	}

	contract := contract.NewContract(ethgo.HexToAddress(address), contractABI, opts...)

	return &Contract{
		Contract:      contract,
		Client:        c,
		SignerAddress: wallet.Address().String(),
	}, nil
}

// DeployContract deploys a contract to the blockchain.
func (c *Client) DeployContract(abistr string, bytecode string, args ...interface{}) (*ethgo.Receipt, error) {
	// Parse ABI
	contractABI, err := abi.NewABI(abistr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse abi: %w", err)
	}

	// Parse bytecode
	contractBytecode, err := hex.DecodeString(bytecode)
	if err != nil {
		return nil, fmt.Errorf("failed to decode bytecode: %w", err)
	}

	opts := []contract.ContractOption{
		contract.WithJsonRPC(c.client.Eth()),
		contract.WithSender(c.w),
	}

	// Deploy contract
	txn, err := contract.DeployContract(contractABI, contractBytecode, args, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy contract: %w, args: %s", err, args)
	}

	blockNumber, err := c.BlockNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to get block number: %w", err)
	}
	block, err := c.GetBlockByNumber(ethgo.BlockNumber(blockNumber), false)
	if err != nil {
		return nil, fmt.Errorf("failed to get block: %w", err)
	}

	txn.WithOpts(&contract.TxnOpts{
		GasLimit: block.GasLimit,
	})

	err = txn.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to deploy contract txn.Do() stage: %w", err)
	}

	receipt, err := txn.Wait()
	if err != nil {
		return nil, fmt.Errorf("failed waiting to deploy contract: %w", err)
	}

	return receipt, nil
}

var blocks sync.Map

// PollBlocks polls for new blocks and emits a "block" metric.
func (c *Client) pollForBlocks() {
	var lastBlockNumber uint64
	var prevBlock *ethgo.Block

	now := time.Now()

	for range time.Tick(500 * time.Millisecond) {
		blockNumber, err := c.BlockNumber()
		if err != nil {
			fmt.Println("failed to get block number: ", err)
			continue
		}

		if blockNumber > lastBlockNumber {
			// compute precise block time
			blockTime := time.Since(now)
			now = time.Now()

			block, err := c.GetBlockByNumber(ethgo.BlockNumber(blockNumber), false)
			if err != nil {
				fmt.Println("failed to get block: ", err)
				continue
			}
			if block == nil {
				// We're not going to continue past this point if we don't have a block
				continue
			}
			lastBlockNumber = blockNumber

			var blockTimestampDiff time.Duration
			var tps float64

			if prevBlock != nil {
				// compute block time
				blockTimestampDiff = time.Unix(int64(block.Timestamp), 0).Sub(time.Unix(int64(prevBlock.Timestamp), 0))
				// Compute TPS
				tps = float64(len(block.TransactionsHashes)) / float64(blockTimestampDiff.Seconds())
			}

			prevBlock = block

			rootTS := metrics.NewRegistry().RootTagSet()
			if c.vu != nil && c.vu.State() != nil && rootTS != nil {
				if _, loaded := blocks.LoadOrStore(c.opts.ProxyURL+strconv.FormatUint(blockNumber, 10), true); loaded {
					// We already have a block number for this client, so we can skip this
					continue
				}

				metrics.PushIfNotDone(c.vu.Context(), c.vu.State().Samples, metrics.ConnectedSamples{
					Samples: []metrics.Sample{
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.Block,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"transactions": strconv.Itoa(len(block.TransactionsHashes)),
									"gas_used":     strconv.Itoa(int(block.GasUsed)),
									"gas_limit":    strconv.Itoa(int(block.GasLimit)),
								}),
							},
							Value: float64(blockNumber),
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.GasUsed,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"block": strconv.Itoa(int(blockNumber)),
								}),
							},
							Value: float64(block.GasUsed),
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.TPS,
								Tags:   rootTS,
							},
							Value: tps,
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.BlockTime,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"block_timestamp_diff": blockTimestampDiff.String(),
								}),
							},
							Value: float64(blockTime.Milliseconds()),
							Time:  time.Now(),
						},
					},
				})
			}
		}
	}
}

type Tracer struct {
	url         string
}

type RequestParams struct {
	requestType string
	method      string
	params      []interface{}
}

func (c *Client) CallTracer(r *RequestParams) (*http.Response, error) {
	contentType := "application/json"
	body := []byte(fmt.Sprintf(`{
		"requestType": %q,
		"method": %q,
		"params": %v,
	}`, r.requestType, r.method, r.params))

	client := &http.Client{}
	req, err := http.NewRequest("POST", c.tracer.url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return resp, nil
}
