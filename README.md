# ergoPool

a mining pool for ergoplatform.org

this is forked from open-ethereum-pool

## Haven't done

Unlocker and Payer havn't finished. So just treat it as a **proxy**.

## How to build

```bash
git clone https://github.com/maoxs2/ergoPool
cd ergoPool

# Run the ergo node, open rpc
nano config.json

go build .
./ergoPool

```

## How to use miner

compile [this miner](https://github.com/maoxs2/Autolykos-GPU-miner) with pool key and distribute to your miners

## Future Develop

I will contine develop if anyone need this. It's not difficult to add payer and unlocker.

If nobody, I'm staying out.
