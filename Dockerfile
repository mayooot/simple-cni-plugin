FROM alpine
RUN apk update && apk add --no-cache iptables
WORKDIR /
COPY  ./bin/* /
