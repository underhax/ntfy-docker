FROM binwiederhier/ntfy:v2.37.2@sha256:1875ddcf815c8a3c981d6d78c67a93452d0abba79b1ab9520b1f5f5238902bd7 AS donor

FROM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS go-builder

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
