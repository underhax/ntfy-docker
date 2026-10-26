FROM binwiederhier/ntfy:v2.34.0@sha256:901e14503d3f605f445be0e68f2ac71f0aa73391897ce6ceb599f8c48d8b3792 AS donor

FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS go-builder

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
