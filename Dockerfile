FROM golang:1.16.6-alpine AS builder

RUN apk add --update --no-cache build-base git mercurial

RUN mkdir -p /build
WORKDIR /build

COPY go.* /build/
COPY pkg/sdk/go.* /build/pkg/sdk/
RUN go mod download

COPY . /build
RUN go install ./cmd/template
RUN go install ./cmd/bank-vaults


FROM alpine:3.14.0

RUN apk add --no-cache ca-certificates curl \
                       ccid opensc pcsc-lite-libs softhsm

USER 65534

# Initializing SoftHSM to be able to create a working example (only for dev),
# sharing the HSM device is emulated with a pre-created keypair in the image.
RUN softhsm2-util --init-token --free --label bank-vaults --so-pin banzai --pin banzai
RUN pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so --keypairgen --key-type rsa:2048 --pin banzai --token-label bank-vaults --label bank-vaults

COPY --from=builder /go/bin/template /usr/local/bin/template
COPY --from=builder /go/bin/bank-vaults /usr/local/bin/bank-vaults
COPY --from=builder /build/scripts/pcscd-entrypoint.sh /usr/local/bin/pcscd-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/bank-vaults"]
