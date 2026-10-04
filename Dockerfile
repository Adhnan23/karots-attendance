# ---- build: static, pure-Go binary (no cgo) ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /attendance ./cmd/attendance && \
    mkdir /data

# ---- run: empty image, just the binary (templates, CSS, migrations, tzdata are embedded) ----
FROM scratch
COPY --from=build /attendance /attendance
COPY --from=build --chown=1000:1000 /data /data
ENV TZ=Asia/Colombo
USER 1000:1000
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/attendance", "-db", "/data/attendance.db"]
