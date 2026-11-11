# Deploy the EIP-7702 executor

Deploy Sepolia first. Use that pin to exercise the CLI. Then deploy the same contract on each EVM chain you need, fund the deployer on that chain, and pin that chain's own address.

`contracts/test/EIP7702Executor.t.sol` is not a deploy script. `forge test` runs it inside Foundry's local EVM. The contract that goes on-chain is `contracts/src/EIP7702Executor.sol`.

The CLI does not read an address from the Solidity folder. After each deploy, paste `eip7702Executor` and `eip7702ExecutorCodeHash` into `src/networks.json` for that chain. An empty pin keeps the 7702 path off. Transfers still send as ordinary transactions.

## Scope

EIP-7702 applies to Ethereum and EVM L2s that expose type-4 transactions. It does not apply to other VMs.

| Supported (EVM) | Not supported |
|---|---|
| Ethereum, Sepolia, BSC, Base, Optimism, Arbitrum, Polygon, and other EVM chains in `src/networks.json` | Solana (SVM), Tron (TVM), Sui / Aptos (MoveVM) |

## 1. Install Foundry

Follow the official install guide: [Foundry installation](https://www.getfoundry.sh/introduction/installation).

Then confirm the tools are on your `PATH`:

```bash
forge --version
cast --version
```

## 2. Build and test the contract

Open a shell in the `contracts/` directory of this repo. On WSL that may look like:

```bash
cd ~/multi-wallet-cli/contracts
```

Install the test library, compile, and run the suite:

```bash
forge install foundry-rs/forge-std
forge build
forge test -vv
```

`forge build` must finish with a successful compiler run. `forge test -vv` must report:

```text
Suite result: ok. 3 passed; 0 failed; 0 skipped;
```

`contracts/foundry.toml` pins solc `0.8.30`, optimizer 200 runs, `bytecode_hash = "none"`, and `evm_version = "prague"`. Leave those settings. A different compiler or optimizer changes the runtime bytes, and the hash you pin later will not match.

## 3. Create or import the deployer account

Use a new deployer wallet. Do not use a wallet that already holds user funds. Fund that address with native gas on each chain you will deploy to. Sepolia ETH does not pay for BSC or Ethereum mainnet.

Either create a fresh key:

```bash
cast wallet new
```

Or import an existing private key into a Foundry keystore named `deployer` (you will be prompted for the key and a keystore password):

```bash
cast wallet import deployer --interactive
```

Confirm the address:

```bash
cast wallet address --account deployer
```

Do not write the private key into the repo, `.env`, or `networks.json`.

## 4. Declare helpers once (WSL / bash)

From `contracts/`, paste this block once in the shell. Replace `DEPLOYER` with the address from the previous step. If an RPC fails, swap it for another `seedRpcs` URL from `src/networks.json` for that chain.

```bash
DEPLOYER=<your_wallet_address>
RPC_SEPOLIA=https://ethereum-sepolia-rpc.publicnode.com
RPC_ETH=https://ethereum-rpc.publicnode.com
RPC_BSC=https://bsc-rpc.publicnode.com
RPC_BASE=https://mainnet.base.org
RPC_OP=https://mainnet.optimism.io
RPC_ARB=https://arb1.arbitrum.io/rpc
RPC_POLY=https://polygon-bor-rpc.publicnode.com

precheck() {  # $1=name  $2=rpc  $3=expected chain id
  echo "== $1 =="
  echo "chain id : $(cast chain-id --rpc-url "$2")  (expected $3)"
  echo "balance  : $(cast balance $DEPLOYER --rpc-url "$2" --ether)"
  echo "gas price: $(cast gas-price --rpc-url "$2")"
}

deploy() {  # $1=rpc
  forge create src/EIP7702Executor.sol:EIP7702Executor \
    --rpc-url "$1" --account deployer --broadcast
}

pin() {  # $1=deployed address  $2=rpc
  cast code "$1" --rpc-url "$2" | head -c 80; echo
  echo "codeHash: $(cast keccak $(cast code "$1" --rpc-url "$2"))"
}
```

There is no constructor argument. `deploy` asks for the keystore password. Copy the `Deployed to:` address after each create. Addresses differ per chain because the deployer nonce differs per chain. Do not copy the Sepolia address onto Ethereum or BSC.

## 5. Per-chain order: precheck → deploy → pin

For every chain, run in this order:

1. `precheck` — chain id must match, balance must be greater than 0.
2. `deploy` — broadcast the create transaction.
3. `pin` — read live `eth_getCode` and print `codeHash` for `networks.json`.

Sepolia first. Finish CLI checks there before mainnets.

```bash
# 1) Sepolia
precheck sepolia $RPC_SEPOLIA 11155111
deploy $RPC_SEPOLIA
pin <DEPLOYED_ADDRESS> $RPC_SEPOLIA

# 2) BSC
precheck bsc $RPC_BSC 56
deploy $RPC_BSC
pin <DEPLOYED_ADDRESS> $RPC_BSC

# 3) Ethereum
precheck ethereum $RPC_ETH 1
deploy $RPC_ETH
pin <DEPLOYED_ADDRESS> $RPC_ETH

# 4) Base / Optimism / Arbitrum / Polygon — same pattern
precheck base     $RPC_BASE 8453
precheck optimism $RPC_OP   10
precheck arbitrum $RPC_ARB  42161
precheck polygon  $RPC_POLY 137
# then: deploy $RPC_xxx && pin <DEPLOYED_ADDRESS> $RPC_xxx for each chain
```

`pin` hashes the live runtime bytecode from `eth_getCode`, not the creation bytecode and not the hex text as a string. The printed `codeHash` is `0x` plus 64 hex characters.

## 6. Paste the pin into `src/networks.json`

Edit the chain object under `contracts`. Leave `wrappedNative` as it is. Example for Sepolia (`ethereum-sepolia`):

```json
"ethereum-sepolia": {
  "contracts": {
    "wrappedNative": "",
    "eip7702Executor": "0xDeployedAddress",
    "eip7702ExecutorCodeHash": "0xkeccak256OfRuntimeCode"
  }
}
```

Repeat for each chain you deployed (`ethereum`, `bsc`, `base`, `optimism`, `arbitrum`, `polygon`, …), using that chain's own address and hash.

| JSON field | Meaning |
|---|---|
| `contracts.eip7702Executor` | Address of this deploy. One address per chain. |
| `contracts.eip7702ExecutorCodeHash` | `keccak256` of that address's runtime code. |

If either value is missing, 7702 stays off for that chain. If the hash does not match live code, that chain errors at plan time and the CLI will not sign an authorization against the mismatched code.

Restart the CLI after editing `networks.json`.

## 7. How the three transfer modes use it

Thresholds live in `src/networks.json` under `_metadata.transfer`: `eip7702MinCalls` is 3, `eip7702MinCallsWhenDelegated` is 2. The diagrams in `public/assets/` are the same rules.

| Mode | Diagram | What runs after the pin is set |
|---|---|---|
| One wallet to one wallet, several tokens | `public/assets/mode_1_to_1.svg` | One source wallet. Each token is one call. 1–2 calls are ordinary transactions. 3 or more calls are one type-4 transaction whose `execute(calls)` sends every token. |
| One wallet to many wallets | `public/assets/mode_1_to_many.svg` | Same owner, so the calls are one batch. Under the threshold, ordinary nonces. At the threshold, one 7702 broadcast. |
| Many wallets to one wallet | `public/assets/mode_many_to_1.svg` | Each source wallet is its own owner and runs in parallel. 7702 does not merge several EOAs into one signature. A send error stops only the later nonces of that owner. |

Until a chain's pin is saved, transfers on that chain follow the ordinary-transaction side of those diagrams only. On Sepolia, a one-to-one transfer of three or more tokens from one unlocked wallet is the check that the green branch in `mode_1_to_1.svg` is live.
