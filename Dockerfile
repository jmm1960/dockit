FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /dockit .

FROM --platform=$BUILDPLATFORM alpine:3.23 AS certificates
RUN apk add --no-cache ca-certificates tzdata

FROM scratch
LABEL dockit.self="true"
LABEL org.opencontainers.image.source="https://github.com/jmm1960/dockit"
LABEL org.opencontainers.image.licenses="Apache-2.0"
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certificates /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /dockit /dockit
ENTRYPOINT ["/dockit"]
