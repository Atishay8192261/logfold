# Build a fully static binary — no CGO, no third-party modules.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/praxis ./cmd/praxis

# Distroless: just the static binary.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/praxis /praxis
# Mount your workspace at /work.
WORKDIR /work
EXPOSE 7777
ENTRYPOINT ["/praxis"]
CMD ["serve", "-addr", ":7777", "-C", "/work"]
