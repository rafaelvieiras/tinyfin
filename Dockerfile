FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/tinyfin ./cmd/tinyfin

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tinyfin /tinyfin
VOLUME ["/data"]
EXPOSE 8096
ENTRYPOINT ["/tinyfin"]
CMD ["-data", "/data/tinyfin.db", "-addr", ":8096"]
