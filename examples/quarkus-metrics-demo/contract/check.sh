#!/bin/sh
set -eu
test -f before.prom; test -f after-traffic.prom; test -f after-restart.prom
grep -q 'status="404"' after-traffic.prom
grep -q 'status="418"' after-traffic.prom
grep -q 'status="500"' after-traffic.prom
grep -q 'process_start_time_seconds' after-restart.prom
