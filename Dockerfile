FROM binwiederhier/ntfy:v2.33.1@sha256:29bc952294d58957f6ca6638eca9a653a33555989224edbdf4bb9433ee606497 AS donor

FROM golang:1.27.3-alpine3.24@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS go-builder

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
