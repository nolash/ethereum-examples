# sample pss protocol

This example illustrates how to implement a protocol of medium complexity using `pss`.

The `main.go` driver implements the protocol on a normal `devp2p` connection. 

The `main_pss.go` driver implements the same protocol on over `pss`.

Files in `service` and `protocol` implement the protocol itself, and are shared between both drivers. This way, the extra implmentation needed for `pss` should be clear.

Its currently based on the branch on https://github.com/nolash/go-ethereum/tree/sos18-demo-2
