package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	solana "github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
)

type Result struct {
	Symbol   string
	Raw      *big.Int
	Decimals uint8
	Err      error
}
type Client struct{ httpClient *http.Client }

func NewClient() *Client { return &Client{httpClient: &http.Client{Timeout: 20 * time.Second}} }

func (c *Client) Scan(ctx context.Context, rpc, address, nativeSymbol string) (Result, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "getBalance", "params": []any{address}}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("encode Solana RPC: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc, strings.NewReader(string(body)))
	if err != nil {
		return Result{}, fmt.Errorf("create Solana RPC request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call Solana RPC: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Result{}, fmt.Errorf("Solana RPC returned HTTP %s", res.Status)
	}
	var response struct {
		Result struct {
			Value uint64 `json:"value"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return Result{}, fmt.Errorf("decode Solana RPC: %w", err)
	}
	if response.Error != nil {
		return Result{}, fmt.Errorf("Solana balance: %s", response.Error.Message)
	}
	return Result{Symbol: nativeSymbol, Raw: new(big.Int).SetUint64(response.Result.Value), Decimals: 9}, nil
}

// RecentBlockhash reads getLatestBlockhash from rpc.
func (c *Client) RecentBlockhash(ctx context.Context, rpc string) (string, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "getLatestBlockhash", "params": []any{map[string]string{"commitment": "finalized"}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Solana blockhash: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("create Solana blockhash request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call Solana blockhash: %w", err)
	}
	defer res.Body.Close()
	var response struct {
		Result struct {
			Value struct {
				Blockhash string `json:"blockhash"`
			} `json:"value"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decode Solana blockhash: %w", err)
	}
	if response.Error != nil {
		return "", fmt.Errorf("Solana blockhash: %s", response.Error.Message)
	}
	if strings.TrimSpace(response.Result.Value.Blockhash) == "" {
		return "", fmt.Errorf("Solana blockhash is empty")
	}
	return response.Result.Value.Blockhash, nil
}

// SendTransaction submits a base64 signed transaction and returns its signature.
func (c *Client) SendTransaction(ctx context.Context, rpc string, signedBase64 string) (string, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "sendTransaction", "params": []any{signedBase64, map[string]string{"encoding": "base64"}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Solana send: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("create Solana send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call Solana send: %w", err)
	}
	defer res.Body.Close()
	var response struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decode Solana send: %w", err)
	}
	if response.Error != nil {
		return "", fmt.Errorf("Solana broadcast: %s", response.Error.Message)
	}
	if response.Result == "" {
		return "", fmt.Errorf("Solana broadcast returned empty signature")
	}
	return response.Result, nil
}

// BuildNativeTransfer builds and signs a SOL transfer transaction.
func BuildNativeTransfer(fromPrivateKey []byte, fromAddress string, toAddress string, lamports uint64, blockhash string) (string, error) {
	privateKey, from, to, hash, err := transferKeys(fromPrivateKey, fromAddress, toAddress, blockhash)
	if err != nil {
		return "", err
	}
	transfer := system.NewTransferInstruction(lamports, from, to)
	built, buildErr := transfer.ValidateAndBuild()
	if buildErr != nil {
		return "", fmt.Errorf("build SOL transfer: %w", buildErr)
	}
	tx, err := solana.NewTransaction([]solana.Instruction{built}, hash)
	if err != nil {
		return "", fmt.Errorf("build SOL transaction: %w", err)
	}
	if _, err := tx.Sign(signerFor(privateKey, from)); err != nil {
		return "", fmt.Errorf("sign SOL transaction: %w", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode SOL transaction: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// BuildSPLTransfer builds and signs an SPL token transfer between ATAs.
func BuildSPLTransfer(fromPrivateKey []byte, fromAddress string, toAddress string, mint string, amount uint64, blockhash string, createDestination bool) (string, error) {
	privateKey, from, to, hash, err := transferKeys(fromPrivateKey, fromAddress, toAddress, blockhash)
	if err != nil {
		return "", err
	}
	mintKey, err := solana.PublicKeyFromBase58(strings.TrimSpace(mint))
	if err != nil {
		return "", fmt.Errorf("invalid SPL mint")
	}
	sourceATA, _, err := solana.FindAssociatedTokenAddress(from, mintKey)
	if err != nil {
		return "", fmt.Errorf("resolve source ATA: %w", err)
	}
	destinationATA, _, err := solana.FindAssociatedTokenAddress(to, mintKey)
	if err != nil {
		return "", fmt.Errorf("resolve destination ATA: %w", err)
	}
	instructions := make([]solana.Instruction, 0, 2)
	if createDestination {
		create := associatedtokenaccount.NewCreateInstruction(from, to, mintKey)
		createInstruction, err := create.ValidateAndBuild()
		if err != nil {
			return "", fmt.Errorf("build create ATA: %w", err)
		}
		instructions = append(instructions, createInstruction)
	}
	move := token.NewTransferInstruction(amount, sourceATA, destinationATA, from, nil)
	moveInstruction, err := move.ValidateAndBuild()
	if err != nil {
		return "", fmt.Errorf("build SPL transfer: %w", err)
	}
	instructions = append(instructions, moveInstruction)
	tx, err := solana.NewTransaction(instructions, hash)
	if err != nil {
		return "", fmt.Errorf("build SPL transaction: %w", err)
	}
	if _, err := tx.Sign(signerFor(privateKey, from)); err != nil {
		return "", fmt.Errorf("sign SPL transaction: %w", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode SPL transaction: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func signerFor(privateKey solana.PrivateKey, from solana.PublicKey) func(solana.PublicKey) *solana.PrivateKey {
	return func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			copied := solana.PrivateKey(append([]byte(nil), privateKey...))
			return &copied
		}
		return nil
	}
}

func transferKeys(fromPrivateKey []byte, fromAddress string, toAddress string, blockhash string) (solana.PrivateKey, solana.PublicKey, solana.PublicKey, solana.Hash, error) {
	if len(fromPrivateKey) != 64 {
		return nil, solana.PublicKey{}, solana.PublicKey{}, solana.Hash{}, fmt.Errorf("Solana private key must be 64 bytes")
	}
	privateKey := solana.PrivateKey(append([]byte(nil), fromPrivateKey...))
	from, err := solana.PublicKeyFromBase58(strings.TrimSpace(fromAddress))
	if err != nil {
		return nil, solana.PublicKey{}, solana.PublicKey{}, solana.Hash{}, fmt.Errorf("invalid sender address")
	}
	if !privateKey.PublicKey().Equals(from) {
		return nil, solana.PublicKey{}, solana.PublicKey{}, solana.Hash{}, fmt.Errorf("sender address does not match private key")
	}
	to, err := solana.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return nil, solana.PublicKey{}, solana.PublicKey{}, solana.Hash{}, fmt.Errorf("invalid recipient address")
	}
	hash := solana.MustHashFromBase58(strings.TrimSpace(blockhash))
	return privateKey, from, to, hash, nil
}
