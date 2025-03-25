# Mint Management Server

The Gonuts mint can optionally be run with a management server. You can enable it be setting the following variable in the `.env` file when running the mint:
```
ENABLE_ADMIN_SERVER=TRUE
```

The management server is exposed over Unix domain sockets so it is only accessible from the same machine where it is running. It comes with the `mint-cli` tool
that you can install by doing:

**If you are at the root directory of the project**

```
go install ./cmd/mint/mint-cli
```

## Functionality and commands available

- **Issued Ecash**: Retrieves the total amount of issued ecash.
    - `--keyset`: Optional to get the issued ecash for a specific keyset.
```
mint-cli issued [--keyset keyset_id]
```

- **Redeemed Ecash**: Retrieves the total redeemed ecash or optionally gets the redeemed ecash for a specified keyset.
    - `--keyset`: Optional to get the redeemed ecash for a specific keyset.
```
mint-cli redeemed [--keyset keyset_id]
```

- **Total Balance**: Provides a summary of issued and redeemed ecash, and total in circulation.
```
mint-cli totalbalance
```

- **List Keysets**: Lists all keysets with their IDs, units, active status and fees.
```
mint-cli keysets
```

- **Rotate Keyset**: Rotates the current active keyset.
    - `--fee`: Required. Specifies the fee for the new keyset.
```
mint-cli rotatekeyset --fee amount
```
