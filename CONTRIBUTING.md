# Contributing

Thanks for looking at Multi-Wallet CLI. Issues and pull requests are welcome.

## Local check

Commands run from `src/`:

```bash
cd src
go test ./...
```

`go test` needs `CGO_ENABLED=0` on machines without a C compiler.

## What not to commit

- Wallet JSON files, the master password, and private keys.
- The keyfile. The first `go run main.go -setup` writes it outside the repo (`keyfile.bin`).
- `.env`, RPC URLs that contain API keys, and transaction logs.

## Code

- Go code lives under `src/`. The executor contract lives under `contracts/`.
- Follow the layer notes in `ai-context/03-architecture.md`.
- Amounts that move value stay `*big.Int`. User-facing strings go through `src/infra/i18n`.
- A behavior change for EVM transfer batching belongs in `spec/features/transfer.md` and `spec/adr/0001-evm-transfer-batching.md` before the code changes.

## Pull requests

Describe what changed and how you checked it. If you could not run a chain, say so. Do not claim a Sepolia or mainnet deploy that is not pinned in `src/networks.json`.
