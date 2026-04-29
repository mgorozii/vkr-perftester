FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/loadtestd ./cmd/loadtestd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/loadtestd /loadtestd
ENTRYPOINT ["/loadtestd"]
