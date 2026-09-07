#!/usr/bin/env bash

#CERT=/etc/rc-server/server.crt    # 改成第 1 步查到的实际路径
#KEY=/etc/rc-server/server.key
mkdir -p ./certs
CERT=./certs/server.crt    # 改成第 1 步查到的实际路径
KEY=./certs/server.key
openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 825 \
  -keyout "$KEY" -out "$CERT" \
  -subj "/CN=rc-server/O=rc" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:103.42.30.173" \
  -addext "basicConstraints=critical,CA:TRUE"
#chmod 600 "$KEY"
#
## 复核确实含公网 IP
#openssl x509 -in "$CERT" -noout -ext subjectAltName
#
#systemctl restart rc-server
