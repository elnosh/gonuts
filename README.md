[![Go Reference](https://pkg.go.dev/badge/github.com/elnosh/gonuts.svg)](https://pkg.go.dev/github.com/elnosh/gonuts)

_Disclaimer: The author is NOT a cryptographer and this work has not been reviewed. This means that there is very likely a fatal flaw somewhere. Cashu is still experimental and not production-ready._

_**Don't be reckless:** This project is in early development, it does however work with real sats! Always use amounts you don't mind losing._

# gonuts

Cashu wallet and mint implementation in Go.

Cashu is a free and open-source Chaumian ecash system built for Bitcoin. You can read more about it [here](https://cashu.space/).

# Supported NUTs

Implemented [NUTs](https://github.com/cashubtc/nuts/):

- [x] [NUT-00](https://github.com/cashubtc/nuts/blob/main/00.md)
- [x] [NUT-01](https://github.com/cashubtc/nuts/blob/main/01.md)
- [x] [NUT-02](https://github.com/cashubtc/nuts/blob/main/02.md)
- [x] [NUT-03](https://github.com/cashubtc/nuts/blob/main/03.md)
- [x] [NUT-04](https://github.com/cashubtc/nuts/blob/main/04.md)
- [x] [NUT-05](https://github.com/cashubtc/nuts/blob/main/05.md)
- [x] [NUT-06](https://github.com/cashubtc/nuts/blob/main/06.md)
- [x] [NUT-07](https://github.com/cashubtc/nuts/blob/main/07.md)
- [x] [NUT-08](https://github.com/cashubtc/nuts/blob/main/08.md) (Wallet only)
- [x] [NUT-09](https://github.com/cashubtc/nuts/blob/main/09.md)
- [x] [NUT-10](https://github.com/cashubtc/nuts/blob/main/10.md)
- [x] [NUT-11](https://github.com/cashubtc/nuts/blob/main/11.md)
- [x] [NUT-12](https://github.com/cashubtc/nuts/blob/main/12.md)
- [x] [NUT-13](https://github.com/cashubtc/nuts/blob/main/13.md)
- [x] [NUT-14](https://github.com/cashubtc/nuts/blob/main/14.md)
- [x] [NUT-15](https://github.com/cashubtc/nuts/blob/main/15.md)
- [x] [NUT-17](https://github.com/cashubtc/nuts/blob/main/17.md)
- [ ] [NUT-18](https://github.com/cashubtc/nuts/blob/main/18.md)
- [x] [NUT-19](https://github.com/cashubtc/nuts/blob/main/19.md)
- [x] [NUT-20](https://github.com/cashubtc/nuts/blob/main/20.md)

# Installation

With [Go](https://go.dev/doc/install) installed, you can run the following command to install the wallet:

```
git clone https://github.com/elnosh/gonuts
cd gonuts
go install ./cmd/nutw/
```

To setup a mint for the wallet, create a `.env` file at ~/.gonuts/wallet/.env and setup your preferred mint.

### Check balance

```
nutw balance
```

### Create a Lightning invoice to receive ecash

```
nutw mint 100
```

This will get an invoice from the mint which you can then pay and use to mint new ecash.

```
invoice: lnbc100n1pja0w9pdqqx...
```

### Redeem the ecash after paying the invoice

```
nutw mint --invoice lnbc100n1pja0w9pdqqx...
```

### Send tokens

```
nutw send 21
```

This will generate a Cashu token that looks like this:

```
cashuAeyJ0b2tlbiI6W3sibW...
```

This is the ecash that you can then send to anyone.

### Receive tokens

```
nutw receive cashuAeyJ0b2tlbiI6W3...
```

### Request the mint to pay a Lightning invoice

```
nutw pay lnbc100n1pju35fedqqsp52xt3...
```

## Running a mint

- Copy the example mint enviornment file
  - `cp .env.mint.example cmd/mint/.env`
- Change to that directory
  - `cd cmd/mint`
- Edit the .env file to configure your mint
  - `nano .env`

#### Creating a rune for CLN

For security best practices, you should issue a new rune for the mint to use that has restrictions so it can only use the methods required by a mint

The mint still allows the `pay` method, so KEEP THIS RUNE SECURE. It can be used to pay invoices from your node.

There are a few ways of doing this.

- `lightning-cli`
  - Use the cli to issue a new rune with restrictions
  ```bash
  lightning-cli createrune -k "restrictions"='[["method=invoice"],["method=getinfo"],["method=listinvoices"],["method=pay"],["method=listpays"],["method=waitinvoice"]]'
  ```
- You can visit the front end of your REST API (`127.0.0.1:3010` is common) and use the gui to log in with your master rune (`lightning-cli showrunes` to show all your runes). After you are logged in, you can use the gui to make a call to your rest server to create a new rune
  - Under the green POST Dropdown enter
    - **rpc_method :** `createrune`
    - **payload :** `{"restrictions": [["method=invoice"]]}`
- Lastly, you can make a curl request from a terminal (this does the same thing as your gui does above)
  ```bash
  curl -X 'POST' \
  'http://127.0.0.1:3010/v1/createrune' \
  -H 'accept: application/json' \
  -H 'Rune: YOUR_MASTER_RUNE_HERE' \
  -H 'Content-Type: application/json' \
  -d '{"restrictions": [["method=invoice"],["method=getinfo"],["method=listinvoices"],["method=pay"],["method=listpays"],["method=waitinvoice"]]}'
  ```

In all of these cases you should get a response with the rune and it's summary

```bash
{
  "rune": "THIS_IS_YOUR_MINT_RUNE",
  "unique_id": "1"
}
```

- save the rune in a file, and keep it somewhere secure

After you have configured the mint, build the executable from the cmd/mint directory

- `go build -v -o mint mint.go`

Run your mint !

- `./mint`

# Development

## Requirements

- [Go](https://go.dev/doc/install)

### Wallet

- `cd cmd/nutw`
- create `.env` file and fill in the values
- `go build -v -o nutw nutw.go`

### Run mint

- `cd cmd/mint`
- you'll need to setup a lightning regtest environment with something like [Polar](https://lightningpolar.com/) and fill in the values in the `.env` file

- `go build -v -o mint mint.go`

- `./mint`

## Contribute

All contributions are welcome.

If you want to contribute, please open an Issue or a PR.
