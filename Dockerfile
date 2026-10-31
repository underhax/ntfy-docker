FROM binwiederhier/ntfy:v2.35.0@sha256:6f0c9abab7b99a978d816611cc90dabb4849b81f60c53114e91cd07931085dc0 AS donor

FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS go-builder

WORKDIR /build

COPY go.mod ./
COPY init/ ./init/
COPY probe/ ./probe/

RUN mkdir -p /out/data /out/etc/ssl/certs /out/usr/local/bin && \
    chmod 0755 /out/etc /out/etc/ssl /out/etc/ssl/certs && \
    chmod 0755 /out/usr /out/usr/local /out/usr/local/bin && \
    chmod 0700 /out/data && \
    cp /etc/ssl/certs/ca-certificates.crt /out/etc/ssl/certs/ca-certificates.crt && \
    chmod 0444 /out/etc/ssl/certs/ca-certificates.crt && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/usr/local/bin/init ./init && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/usr/local/bin/probe ./probe

FROM scratch

COPY --from=go-builder /out/etc /etc
COPY --from=go-builder --chmod=0700 /out/data /data
COPY --from=donor --chmod=0555 /usr/bin/ntfy /usr/local/bin/ntfy
COPY --from=go-builder --chmod=0555 /out/usr/local/bin/init /usr/local/bin/init
COPY --from=go-builder --chmod=0555 /out/usr/local/bin/probe /usr/local/bin/probe

ENTRYPOINT ["/usr/local/bin/init"]
CMD ["serve"]
