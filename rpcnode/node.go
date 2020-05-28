package rpcnode

/**
 * RPC Node Kit
 *
 * What is my purpose?
 * - You connect to a bitcoind node
 * - You make RPC calls for me
 */

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/tokenized/pkg/bitcoin"
	"github.com/tokenized/pkg/logger"
	"github.com/tokenized/pkg/wire"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	btcwire "github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
	"github.com/pkg/errors"
)

const (
	// SubSystem is used by the logger package
	SubSystem = "RPCNode"
)

type RPCNode struct {
	client  *rpcclient.Client
	txCache map[bitcoin.Hash32]*wire.MsgTx
	Config  *Config
	lock    sync.Mutex
}

// NewNode returns a new instance of an RPC node
func NewNode(config *Config) (*RPCNode, error) {
	rpcConfig := rpcclient.ConnConfig{
		HTTPPostMode: true,
		DisableTLS:   true,
		Host:         config.Host,
		User:         config.Username,
		Pass:         config.Password,
	}

	client, err := rpcclient.New(&rpcConfig, nil)
	if err != nil {
		return nil, err
	}

	if config.RetryDelay == 0 { // default to 1/2 second delay
		config.RetryDelay = 500
	}

	n := &RPCNode{
		client:  client,
		Config:  config,
		txCache: make(map[bitcoin.Hash32]*wire.MsgTx),
	}

	return n, nil
}

// IsNotSeenError returns true if the error is because a transaction is not known to the RPC node.
// TODO Determine when error is tx not seen yet --ce
// -5: No such mempool or blockchain transaction. Use gettransaction for wallet transactions.
// -5 = RPC_INVALID_ADDRESS_OR_KEY, which is returned when tx is not found
// Should be formatted as JSON at other end
//   {"code": -5, "message": "No such mempool or blockchain transaction. Use gettransaction for wallet transactions."}
func IsNotSeenError(err error) bool {
	c := errors.Cause(err)
	jsonErr, ok := c.(*btcjson.Error)
	if !ok {
		return false
	}
	return jsonErr.ErrorCode == -5 // RPC_INVALID_ADDRESS_OR_KEY (tx not seen)
}

// GetTX requests a tx from the remote server.
func (r *RPCNode) GetTX(ctx context.Context, id *bitcoin.Hash32) (*wire.MsgTx, error) {
	ctx = logger.ContextWithLogSubSystem(ctx, SubSystem)
	defer logger.Elapsed(ctx, time.Now(), "GetTX")

	r.lock.Lock()
	msg, ok := r.txCache[*id]
	if ok {
		logger.Verbose(ctx, "Using tx from RPC cache : %s\n", id.String())
		delete(r.txCache, *id)
		r.lock.Unlock()
		return msg, nil
	}
	r.lock.Unlock()

	logger.Verbose(ctx, "Requesting tx from RPC : %s\n", id.String())
	ch, _ := chainhash.NewHash(id[:])
	var err error
	var raw *btcjson.TxRawResult
	for i := 0; i <= r.Config.MaxRetries; i++ {
		raw, err = r.client.GetRawTransactionVerbose(ch)
		if err == nil {
			break
		}

		if IsNotSeenError(err) {
			logger.Error(ctx, "RPCTxNotSeenYet GetTxs receive tx %s : %v", id.String(), err)
		} else {
			logger.Error(ctx, "RPCCallFailed GetTx %s : %v", id.String(), err)
		}
		time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted GetTx %s : %v", id.String(), err)
		return nil, errors.Wrap(err, fmt.Sprintf("Failed to GetTx %v", id.String()))
	}

	b, err := hex.DecodeString(raw.Hex)
	if err != nil {
		return nil, err
	}

	tx := wire.MsgTx{}
	buf := bytes.NewReader(b)

	if err := tx.Deserialize(buf); err != nil {
		return nil, err
	}

	return &tx, nil
}

// GetTXs requests a list of txs from the remote server.
func (r *RPCNode) GetTXs(ctx context.Context, txids []*bitcoin.Hash32) ([]*wire.MsgTx, error) {
	ctx = logger.ContextWithLogSubSystem(ctx, SubSystem)
	defer logger.Elapsed(ctx, time.Now(), "GetTXs")

	results := make([]*wire.MsgTx, len(txids))

	r.lock.Lock()
	for i, txid := range txids {
		msg, ok := r.txCache[*txid]
		if ok {
			logger.Verbose(ctx, "Using tx from RPC cache : %s\n", txid.String())
			delete(r.txCache, *txid)
			results[i] = msg
		}
	}
	r.lock.Unlock()

	var lastError error
	for retry := 0; retry <= r.Config.MaxRetries; retry++ {
		if retry != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		requests := make([]*rpcclient.FutureGetRawTransactionVerboseResult, len(txids))

		for i, txid := range txids {
			if results[i] == nil {
				logger.Verbose(ctx, "Requesting tx from RPC : %s\n", txid.String())
				ch, _ := chainhash.NewHash(txid[:])
				request := r.client.GetRawTransactionVerboseAsync(ch)
				requests[i] = &request
			}
		}

		lastError = nil
		for i, request := range requests {
			if request == nil {
				continue
			}

			rawTx, err := request.Receive()
			if err != nil {
				lastError = err
				if IsNotSeenError(err) {
					logger.Error(ctx, "RPCTxNotSeenYet GetTxs receive tx %s : %v", txids[i].String(), err)
				} else {
					logger.Error(ctx, "RPCCallFailed GetTxs receive tx %s : %v", txids[i].String(), err)
				}
				continue
			}

			b, err := hex.DecodeString(rawTx.Hex)
			if err != nil {
				lastError = err
				logger.Error(ctx, "RPCCallFailed GetTxs decode tx hex %s : %v", txids[i].String(), err)
				continue
			}

			tx := wire.MsgTx{}
			buf := bytes.NewReader(b)

			if err := tx.Deserialize(buf); err != nil {
				lastError = err
				logger.Error(ctx, "RPCCallFailed GetTxs deserialize tx %s : %v", txids[i].String(), err)
				continue
			}

			results[i] = &tx
		}

		if lastError == nil {
			break
		}
	}

	if lastError != nil {
		logger.Error(ctx, "RPCCallAborted GetTxs %v : %v", txids, lastError)
	}

	return results, lastError
}

func (r *RPCNode) GetOutputs(ctx context.Context, outpoints []wire.OutPoint) ([]bitcoin.UTXO, error) {
	ctx = logger.ContextWithLogSubSystem(ctx, SubSystem)
	defer logger.Elapsed(ctx, time.Now(), "GetOutputs")

	results := make([]bitcoin.UTXO, len(outpoints))
	filled := make([]bool, len(outpoints))

	r.lock.Lock()
	for i, outpoint := range outpoints {
		tx, ok := r.txCache[outpoint.Hash]
		if ok && len(tx.TxOut) > int(outpoint.Index) {
			logger.Verbose(ctx, "Using tx from RPC cache : %s\n", outpoint.Hash.String())
			delete(r.txCache, outpoint.Hash)
			results[i] = bitcoin.UTXO{
				Hash:          outpoint.Hash,
				Index:         outpoint.Index,
				Value:         tx.TxOut[outpoint.Index].Value,
				LockingScript: tx.TxOut[outpoint.Index].PkScript,
			}
			filled[i] = true
		}
	}
	r.lock.Unlock()

	var lastError error
	for retry := 0; retry <= r.Config.MaxRetries; retry++ {
		if retry != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		requests := make([]*rpcclient.FutureGetRawTransactionVerboseResult, len(outpoints))

		for i, outpoint := range outpoints {
			if !filled[i] {
				logger.Verbose(ctx, "Requesting tx from RPC : %s\n", outpoint.Hash.String())
				ch, _ := chainhash.NewHash(outpoint.Hash[:])
				request := r.client.GetRawTransactionVerboseAsync(ch)
				requests[i] = &request
			}
		}

		lastError = nil
		for i, request := range requests {
			if request == nil {
				continue
			}

			rawTx, err := request.Receive()
			if err != nil {
				lastError = err
				if IsNotSeenError(err) {
					logger.Error(ctx, "RPCTxNotSeenYet GetRawTx receive tx %s : %v",
						outpoints[i].Hash.String(), err)
				} else {
					logger.Error(ctx, "RPCCallFailed GetRawTx receive tx %s : %v",
						outpoints[i].Hash.String(), err)
				}
				continue
			}

			b, err := hex.DecodeString(rawTx.Hex)
			if err != nil {
				lastError = err
				logger.Error(ctx, "RPCCallFailed GetRawTx decode tx hex %s : %v",
					outpoints[i].Hash.String(), err)
				continue
			}

			tx := wire.MsgTx{}
			buf := bytes.NewReader(b)

			if err := tx.Deserialize(buf); err != nil {
				lastError = err
				logger.Error(ctx, "RPCCallFailed GetRawTx deserialize tx %s : %v",
					outpoints[i].Hash.String(), err)
				continue
			}

			outpoint := outpoints[i]

			if int(outpoint.Index) >= len(tx.TxOut) {
				return results, fmt.Errorf("Invalid output index for txid %d/%d : %s",
					outpoint.Index, len(tx.TxOut), outpoint.Hash.String())
			}

			results[i] = bitcoin.UTXO{
				Hash:          outpoint.Hash,
				Index:         outpoint.Index,
				Value:         tx.TxOut[outpoint.Index].Value,
				LockingScript: tx.TxOut[outpoint.Index].PkScript,
			}
			filled[i] = true
		}

		if lastError == nil {
			break
		}
	}

	if lastError != nil {
		logger.Error(ctx, "RPCCallAborted GetRawTx %v : %v", outpoints, lastError)
	}

	return results, lastError
}

// WatchAddress instructs the RPC node to watch an address without rescan
func (r *RPCNode) WatchAddress(ctx context.Context, address bitcoin.Address) error {
	strAddr := address.String()

	// Make address known to node without rescan
	var err error
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		err = r.client.ImportAddressRescan(strAddr, strAddr, false)
		if err == nil {
			break
		}

		logger.Error(ctx, "RPCCallFailed WatchAddress %s : %v", address.String(), err)
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted WatchAddress %s : %v", address.String(), err)
		return errors.Wrap(err, fmt.Sprintf("Failed to GetTx %s", address.String()))
	}

	return err
}

// ListTransactions returns all transactions for watched addresses
func (r *RPCNode) ListTransactions(ctx context.Context) ([]btcjson.ListTransactionsResult, error) {

	// Prepare listtransactions command
	cmd := btcjson.NewListTransactionsCmd(
		btcjson.String("*"),
		btcjson.Int(99999),
		btcjson.Int(0),
		btcjson.Bool(true))

	var err error
	var marshalledJSON []byte
	var response json.RawMessage
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		id := r.client.NextID()
		marshalledJSON, err = btcjson.MarshalCmd(id, cmd)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed ListTransactions MarshalCmd : %v", err)
			continue
		}

		// Unmarhsal in to a request to extract the params
		var request btcjson.Request
		if err = json.Unmarshal(marshalledJSON, &request); err != nil {
			logger.Error(ctx, "RPCCallFailed ListTransactions Unmarshal : %v", err)
			continue
		}

		// Submit raw request
		response, err = r.client.RawRequest("listtransactions", request.Params)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed ListTransactions RawRequest : %v", err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted ListTransactions : %v", err)
		return nil, errors.Wrap(err, "list transactions")
	}

	// Unmarshal response in to a ListTransactionsResult
	var result []btcjson.ListTransactionsResult
	if err = json.Unmarshal(response, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// ListUnspent returns unspent transactions
func (r *RPCNode) ListUnspent(ctx context.Context, address bitcoin.Address) ([]btcjson.ListUnspentResult, error) {

	// Make address known to node without rescan
	if err := r.WatchAddress(ctx, address); err != nil {
		return nil, err
	}

	btcaddress, _ := btcutil.DecodeAddress(address.String(),
		bitcoin.NewChainParams(bitcoin.NetworkName(address.Network())))
	addresses := []btcutil.Address{btcaddress}

	var err error
	var result []btcjson.ListUnspentResult
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		// out []btcjson.ListUnspentResult
		result, err = r.client.ListUnspentMinMaxAddresses(0, 999999, addresses)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed ListUnspent %s : %v", address.String(), err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted ListUnspent %s: %v", address.String(), err)
		return nil, errors.Wrap(err, fmt.Sprintf("Failed to ListUnspent %s", address.String()))
	}

	return result, nil
}

// SendRawTransaction broadcasts a raw transaction
func (r *RPCNode) SendRawTransaction(ctx context.Context, tx *wire.MsgTx) error {

	nx, err := r.txToBtcdTX(tx)
	if err != nil {
		return err
	}

	logger.Debug(ctx, "Sending raw tx payload : %s", r.getRawPayload(nx))

	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		_, err = r.client.SendRawTransaction(nx, false)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed SendRawTransaction %s : %v", tx.TxHash().String(), err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted SendRawTransaction %s : %v", tx.TxHash().String(), err)
		return errors.Wrap(err, fmt.Sprintf("Failed to SendRawTransaction %s", tx.TxHash().String()))
	}

	return nil
}

// SaveTX saves a tx to be used later.
func (r *RPCNode) SaveTX(ctx context.Context, msg *wire.MsgTx) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	ctx = logger.ContextWithLogSubSystem(ctx, SubSystem)
	hash := msg.TxHash()
	logger.Verbose(ctx, "Saving tx to rpc cache : %s\n", hash.String())
	r.txCache[*hash] = msg
	return nil
}

// SendTX sends a tx to the remote server to be broadcast to the P2P network.
func (r *RPCNode) SendTX(ctx context.Context, tx *wire.MsgTx) (*bitcoin.Hash32, error) {

	ctx = logger.ContextWithLogSubSystem(ctx, SubSystem)
	defer logger.Elapsed(ctx, time.Now(), "SendTX")

	nx, err := r.txToBtcdTX(tx)
	if err != nil {
		return nil, err
	}

	logger.Debug(ctx, "Sending tx payload : %s", r.getRawPayload(nx))

	var hash *chainhash.Hash
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		hash, err = r.client.SendRawTransaction(nx, false)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed SendTX %s : %v", tx.TxHash().String(), err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted SendTX %s : %v", tx.TxHash().String(), err)
		return nil, errors.Wrap(err, fmt.Sprintf("Failed to SendRawTransaction %s",
			tx.TxHash().String()))
	}

	return bitcoin.NewHash32(hash[:])
}

func (r *RPCNode) GetLatestBlock(ctx context.Context) (*bitcoin.Hash32, int32, error) {
	var err error
	var hash *chainhash.Hash
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		// Get the best block hash
		hash, err = r.client.GetBestBlockHash()
		if err != nil {
			logger.Error(ctx, "RPCCallFailed GetLatestBlock GetBestBlockHash : %v", err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted GetLatestBlock GetBestBlockHash : %v", err)
		return nil, -1, errors.Wrap(err, "GetBestBlockHash")
	}

	bhash, err := bitcoin.NewHash32(hash[:])
	if err != nil {
		return nil, -1, errors.Wrap(err, "NewHash32")
	}

	var header *btcjson.GetBlockHeaderVerboseResult
	for i := 0; i <= r.Config.MaxRetries; i++ {
		if i != 0 {
			time.Sleep(time.Duration(r.Config.RetryDelay) * time.Millisecond)
		}

		// The height is in the header
		header, err = r.client.GetBlockHeaderVerbose(hash)
		if err != nil {
			logger.Error(ctx, "RPCCallFailed GetLatestBlock GetBlockHeaderVerbose : %v", err)
			continue
		}

		break
	}

	if err != nil {
		logger.Error(ctx, "RPCCallAborted GetLatestBlock GetBlockHeaderVerbose : %v", err)
		return nil, -1, errors.Wrap(err, "GetBlockHeaderVerbose")
	}

	return bhash, header.Height, nil
}

func (r *RPCNode) getRawPayload(tx *btcwire.MsgTx) string {
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return ""
	}

	return hex.EncodeToString(buf.Bytes())
}

// txToBtcdTx converts a "pkg/wire".MsgTx to a "btcsuite/btcd/wire".MsgTx".
func (r *RPCNode) txToBtcdTX(tx *wire.MsgTx) (*btcwire.MsgTx, error) {
	// Read the payload from the input TX, into the output TX.
	var buf bytes.Buffer
	tx.Serialize(&buf)

	reader := bytes.NewReader(buf.Bytes())

	nx := &btcwire.MsgTx{}

	if err := nx.Deserialize(reader); err != nil {
		return nil, err
	}

	return nx, nil
}
