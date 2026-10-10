# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/cotiza ./cmd/cotiza \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cotiza /cotiza
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV COTIZA_DB=/data/cotiza.db \
    COTIZA_ADDR=:8080 \
    COTIZA_SYNC_EVERY=6h
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/cotiza"]
CMD ["serve"]
