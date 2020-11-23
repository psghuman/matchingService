# syntax=docker/dockerfile:experimental

FROM golang:1.14-alpine3.11 AS builder
WORKDIR /go/src/matchingService/matchingService-go
COPY matchingService-go .
RUN CGO_ENABLED=0 GOOS=linux go build -o app .

FROM scratch
WORKDIR /root/
EXPOSE 8080
COPY --from=builder /go/src/matchingService/matchingService-go/app .
CMD ["./app"]
