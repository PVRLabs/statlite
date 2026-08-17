#!/usr/bin/env sh

BASE_URL=${BASE_URL:-http://127.0.0.1:8080}

ok_count=0
bad_count=0
notfound_count=0
error_count=0
other_count=0
unexpected_count=0

request() {
    path=$1
    expected=$2
    code=$(curl --noproxy '*' --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE_URL$path" 2>/dev/null || printf '000')
    if [ "$code" != "$expected" ]; then
        unexpected_count=$((unexpected_count + 1))
    fi
    case $code in
        200) ok_count=$((ok_count + 1)) ;;
        400) bad_count=$((bad_count + 1)) ;;
        404) notfound_count=$((notfound_count + 1)) ;;
        500) error_count=$((error_count + 1)) ;;
        *) other_count=$((other_count + 1)) ;;
    esac
}

i=0
while [ "$i" -lt 20 ]; do
    request /api/hello 200
    i=$((i + 1))
done

i=0
while [ "$i" -lt 5 ]; do
    request /api/db 200
    i=$((i + 1))
done

for delay in 0 50 250 750; do
    request "/api/slow?ms=$delay" 200
done

i=0
while [ "$i" -lt 3 ]; do
    request /api/bad-request 400
    i=$((i + 1))
done

i=0
while [ "$i" -lt 5 ]; do
    request /does-not-exist 404
    i=$((i + 1))
done

i=0
while [ "$i" -lt 3 ]; do
    request /api/error 500
    i=$((i + 1))
done

total_count=$((ok_count + bad_count + notfound_count + error_count + other_count))

printf 'Traffic attempted: 40 requests\n'
printf 'Recipe: /api/hello 20x200; /api/db 5x200; /api/slow?ms=0|50|250|750 4x200; /api/bad-request 3x400; /does-not-exist 5x404; /api/error 3x500\n'
printf '  200 responses: %s\n' "$ok_count"
printf '  400 responses: %s\n' "$bad_count"
printf '  404 responses: %s\n' "$notfound_count"
printf '  500 responses: %s\n' "$error_count"
printf '  other or failed: %s\n' "$other_count"
printf '  counted responses: %s\n' "$total_count"

if [ "$ok_count" -ne 29 ] || [ "$bad_count" -ne 3 ] || \
   [ "$notfound_count" -ne 5 ] || [ "$error_count" -ne 3 ] || \
   [ "$other_count" -ne 0 ] || [ "$total_count" -ne 40 ] || \
   [ "$unexpected_count" -ne 0 ]; then
    printf 'Traffic recipe failed: one or more requests returned an unexpected status.\n' >&2
    exit 1
fi
