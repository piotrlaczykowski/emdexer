#!/bin/bash
/opt/emdexer/gateway/emdexer-gateway >> /var/log/emdexer-gateway.log 2>&1 &
echo $! > /var/run/emdexer-gateway.pid
echo Gateway started PID: $!
