_Disclaimer: The author is NOT a cryptographer and this work has not been reviewed. This means that there is very likely a fatal flaw somewhere. Cashu is still experimental and not production-ready._

# gonuts

[Cashu](https://cashu.space/) wallet and mint implementation in Go.

Cashu is a free and open-source Chaumian ecash system built for Bitcoin.

## Supported NUTs

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

# Development

## requirements

- go

### run mint

- `cd cmd/mint`

- `cp .env.example .env`

  you'll need to setup a lightning regtest environment with something like [Polar](https://lightningpolar.com/) and fill in the values in the .env file

- `go build -v -o mint mint.go`

- `./mint`

### wallet

- `cd cmd/nutw`

fill the values in .env file with the mint to connect to

- `go build -v -o nutw nutw.go`

# using the wallet

### check balance

`./nutw balance`

### create a lightning invoice to receive ecash

`./nutw mint 100`

this will get an invoice from the mint

```
invoice: lnbcrt100n1pjuvtdpp...
```

### redeem the ecash after paying the invoice

`./nutw mint --invoice lnbcrt100n1pjuvtdpp...`

### send tokens

`./nutw send 21`

### receive tokens

`./nutw receive cashuAeyJ0b2tlbiI6W3...`

## Contribute

All contributions are welcome.

If you want to contribute, please open an Issue or a PR.
