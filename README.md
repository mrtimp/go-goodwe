# go-goodwe

Extract GoodWe solar inverter metrics and send them to PVOutput.

Inspired by: https://github.com/borft/py-goodwe

## Usage

Running from the CLI:

```bash
go-goodwe --ip-address [IP address of inverter] --api-key [PVOutput API key] --system-id [PVOutput system ID]
```

Running via Docker:
```bash
docker run \
  -e TZ=[Optionally set the timezone] \
  -e IP_ADDRESS=[IP address of inverter] \
  -e API_KEY=[PVOutput API key] \
  -e SYSTEM_ID=[PVOutput system ID] \
  [Docker Image]
```

## License

Open-sourced software licensed under the [MIT license](https://opensource.org/licenses/MIT).

## Contact

Tim Philips [@mr_timp](https://twitter.com/mr_timp)
