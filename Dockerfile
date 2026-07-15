# Build the server as a small static binary, then ship it in a minimal image.
FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off → fully static binary that runs on distroless/scratch.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

# distroless/static ships CA certificates (needed for HTTPS to the OpenAI and
# Anthropic APIs) and nothing else — no shell, tiny attack surface.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/server /server

# The server reads all config from the environment (DATABASE_URL,
# EMBEDDING_API_KEY, LLM_API_KEY, LLM_MODEL, EMBEDDING_MODEL, PORT). There is no
# .env inside the image — inject these via the platform's secrets/env.
ENV PORT=8090
EXPOSE 8090

ENTRYPOINT ["/server"]
