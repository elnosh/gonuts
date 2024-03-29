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
- [ ] [NUT-06](https://github.com/cashubtc/nuts/blob/main/06.md)
- [ ] [NUT-07](https://github.com/cashubtc/nuts/blob/main/07.md)
- [ ] [NUT-08](https://github.com/cashubtc/nuts/blob/main/08.md)
- [ ] [NUT-10](https://github.com/cashubtc/nuts/blob/main/10.md)
- [ ] [NUT-11](https://github.com/cashubtc/nuts/blob/main/11.md)
- [ ] [NUT-12](https://github.com/cashubtc/nuts/blob/main/12.md)

# Installation

With [Go](https://go.dev/doc/install) installed, you can run the following command to install the wallet:

```
go install github.com/elnosh/gonuts/cmd/nutw@latest
```

**_Warning_: By default, the wallet will be setup with the mint `https://8333.space:3338`. That is a public test mint for demonstration purposes and could vanish at any moment.**

To setup the wallet with a different mint, create a `.env` file at ~/.gonuts/wallet/.env and setup your preferred mint.

## Using the wallet

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
