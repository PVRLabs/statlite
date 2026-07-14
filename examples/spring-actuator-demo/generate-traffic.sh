#!/usr/bin/env sh

BASE_URL=${BASE_URL:-http://127.0.0.1:8080}

request() {
    path=$1
    code=$(curl --noproxy '*' --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE_URL$path" 2>/dev/null || printf '000')
    case $code in
        200) ok_count=$((ok_count + 1)) ;;
        400) bad_count=$((bad_count + 1)) ;;
        404) notfound_count=$((notfound_count + 1)) ;;
        500) error_count=$((error_count + 1)) ;;
        *) other_count=$((other_count + 1)) ;;
    esac
}

ok_count=0
bad_count=0
notfound_count=0
error_count=0
other_count=0

i=0
while [ "$i" -lt 20 ]; do
    request /api/hello
    i=$((i + 1))
done

i=0
while [ "$i" -lt 5 ]; do
    request /api/db
    i=$((i + 1))
    db_count=$((db_count + 1))
done

for delay in 0 50 250 750; do
    request "/api/slow?ms=$delay"
done

i=0
while [ "$i" -lt 3 ]; do
    request /api/bad-request
    i=$((i + 1))
done

i=0
while [ "$i" -lt 5 ]; do
    request /does-not-exist
    i=$((i + 1))
done

i=0
while [ "$i" -lt 3 ]; do
    request /api/error
    i=$((i + 1))
done

total_count=$((ok_count + bad_count + notfound_count + error_count + other_count))

printf 'Traffic attempted: 40 requests\n'
printf '  200 responses: %s\n' "$ok_count"
printf '  400 responses: %s\n' "$bad_count"
printf '  404 responses: %s\n' "$notfound_count"
printf '  500 responses: %s\n' "$error_count"
printf '  other or failed: %s\n' "$other_count"
printf '  counted responses: %s\n' "$total_count"
