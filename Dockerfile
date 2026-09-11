FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 \
    GOOS="${TARGETOS}" \
    GOARCH="${TARGETARCH}" \
    go build \
      -trimpath \
      -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=${VERSION}" \
      -o /out/statlite \
      ./cmd/statlite

FROM alpine:3.22.1

RUN apk add --no-cache ca-certificates \
    && addgroup -S statlite \
    && adduser -S -D -H -G statlite statlite \
    && mkdir /data \
    && chown statlite:statlite /data

COPY --from=build /out/statlite /usr/local/bin/statlite
COPY docker/statlite.yaml /etc/statlite/statlite.yaml

RUN chown statlite:statlite /etc/statlite/statlite.yaml

USER statlite

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/statlite"]
CMD ["--config", "/etc/statlite/statlite.yaml"]
