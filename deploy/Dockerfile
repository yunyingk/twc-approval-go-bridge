FROM golang:1.24.13-alpine3.22@sha256:3641e0d9b931dc4f2f185dcd669c4679670e9277c8166a838ddb98a2d4389cb5 AS build

ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w -X github.com/yunyingk/twc-approval-go-bridge/internal/version.Version=${VERSION}" \
    -o /out/server ./cmd/server

RUN mkdir -p /out/data && chmod 700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/server /server
COPY --from=build --chown=65532:65532 /out/data /data
ENV CONFIG_FILE=/config.json
EXPOSE 8080
ENTRYPOINT ["/server"]
