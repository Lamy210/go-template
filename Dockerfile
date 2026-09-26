# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS build
WORKDIR /src

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN MODULE_PATH="$(go list -m)" &&     CGO_ENABLED=0 GOOS=linux go build       -trimpath       -ldflags="-s -w -X ${MODULE_PATH}/internal/buildinfo.version=${VERSION} -X ${MODULE_PATH}/internal/buildinfo.commit=${COMMIT} -X ${MODULE_PATH}/internal/buildinfo.buildDate=${BUILD_DATE}"       -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
