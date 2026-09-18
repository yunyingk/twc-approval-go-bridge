FROM golang:1.24.13-alpine AS build

ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src

COPY go.mod ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w -X github.com/yunyingk/twc-approval-go-bridge/internal/version.Version=${VERSION}" \
    -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
