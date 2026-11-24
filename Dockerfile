# Server image (the NDI bridge runs on an on-site computer, not in this image)
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The version comes from the VERSION file (the build has no git history).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(cat VERSION 2>/dev/null || echo dev)" -o /openline-server ./cmd/openline-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /openline-server /openline-server
VOLUME /data
EXPOSE 8999
ENTRYPOINT ["/openline-server", "-data", "/data"]
