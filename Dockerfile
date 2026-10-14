# syntax=docker/dockerfile:1
# Imagem de runtime mínima (distroless). Os CLIs claude/codex NÃO vêm aqui:
# para rodar agentes dentro do container use Dockerfile.agents.
FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# O código gerado (templ/sqlc) é versionado; não precisamos dessas ferramentas aqui.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/hanko ./cmd/hanko

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/hanko /hanko
USER nonroot
EXPOSE 7420
ENV HANKO_ADDR=0.0.0.0:7420
ENTRYPOINT ["/hanko","serve"]
