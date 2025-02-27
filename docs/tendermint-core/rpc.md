---
order: 9
---

# RPC

The RPC documentation is hosted here:

- [https://docs.tendermint.com/master/rpc/](https://docs.tendermint.com/master/rpc/)

To update the documentation, edit the relevant `godoc` comments in the [rpc/core directory](https://github.com/klyed/tendermint/tree/master/rpc/core).

If you are using Tendermint in-process, you will need to set the version to be displayed in the RPC.

If you are using a makefile with your go project, this can be done by using sed and `ldflags`.

Example: 

```
VERSION := $(shell go list -m github.com/klyed/tendermint | sed 's:.* ::')
LD_FLAGS = -X github.com/klyed/tendermint/version.TMCoreSemVer=$(VERSION)

install:
  @echo "Installing the brr machine"
  @go install -mod=readonly -ldflags "$(LD_FLAGS)" ./cmd/<app>
.PHONY: install
```
