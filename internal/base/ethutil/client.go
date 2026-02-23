// Package ethutil provides shared utilities for interacting with Base chain
// via go-ethereum. Mirrors the pattern from agent-inference's zerog/chain.go.
package ethutil

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// LoadKey parses a hex-encoded ECDSA private key (with or without 0x prefix).
func LoadKey(hexKey string) (*ecdsa.PrivateKey, error) {
	hexKey = strings.TrimPrefix(hexKey, "0x")
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("ethutil: invalid private key: %w", err)
	}
	return key, nil
}

// MakeTransactOpts creates signed transaction options for on-chain calls.
func MakeTransactOpts(ctx context.Context, key *ecdsa.PrivateKey, chainID int64) (*bind.TransactOpts, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(chainID))
	if err != nil {
		return nil, fmt.Errorf("ethutil: create transactor: %w", err)
	}
	opts.Context = ctx
	return opts, nil
}

// DialClient connects to an Ethereum-compatible JSON-RPC endpoint.
func DialClient(ctx context.Context, rpcURL string) (*ethclient.Client, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("ethutil: dial %s: %w", rpcURL, err)
	}
	return client, nil
}

// AddressFromKey derives the Ethereum address from a private key.
func AddressFromKey(key *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(key.PublicKey)
}

// SignAndSend builds an EIP-1559 transaction, signs it, submits it, and waits
// for the on-chain receipt. Returns the tx hash and mined receipt.
func SignAndSend(ctx context.Context, client *ethclient.Client, key *ecdsa.PrivateKey, chainID int64, to common.Address, data []byte, value *big.Int) (common.Hash, *types.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: context cancelled: %w", err)
	}

	from := crypto.PubkeyToAddress(key.PublicKey)
	cid := big.NewInt(chainID)

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: get nonce for %s: %w", from.Hex(), err)
	}

	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: suggest gas tip: %w", err)
	}

	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: get latest header: %w", err)
	}

	// gasFeeCap = 2 * baseFee + gasTipCap (standard EIP-1559 formula).
	gasFeeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	gasFeeCap.Add(gasFeeCap, gasTipCap)

	if value == nil {
		value = big.NewInt(0)
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:      from,
		To:        &to,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
		Value:     value,
		Data:      data,
	})
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: estimate gas: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   cid,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      data,
	})

	signer := types.LatestSignerForChainID(cid)
	signedTx, err := types.SignTx(tx, signer, key)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: sign tx: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return common.Hash{}, nil, fmt.Errorf("ethutil: send tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, client, signedTx)
	if err != nil {
		return signedTx.Hash(), nil, fmt.Errorf("ethutil: wait mined for %s: %w", signedTx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return signedTx.Hash(), receipt, fmt.Errorf("ethutil: tx %s reverted", signedTx.Hash().Hex())
	}

	return signedTx.Hash(), receipt, nil
}
