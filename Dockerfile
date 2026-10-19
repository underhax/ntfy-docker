FROM binwiederhier/ntfy:v2.32.0@sha256:59250e1d8be2ee2186c4560322f625faef22ace380331f05b63f8842dbd62602 AS donor

FROM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS go-builder

WORKDIR /build

COPY go.mod ./
COPY init/ ./init/
COPY probe/ ./probe/

RUN mkdir -p /out/data && \
    chmod 0700 /out/data && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/init ./init && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/probe ./probe

FROM scratch

COPY --from=go-builder --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=donor --chmod=0555 /usr/bin/ntfy /usr/local/bin/ntfy
COPY --from=go-builder --chmod=0555 /out/init /usr/local/bin/init
COPY --from=go-builder --chmod=0555 /out/probe /usr/local/bin/probe
COPY --from=go-builder --chmod=0700 /out/data /data

ENTRYPOINT ["/usr/local/bin/init"]
CMD ["serve"]
