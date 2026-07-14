#!/bin/bash
set -e

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
BINARY_NAME="statlite"

function help() {
    echo -e "${CYAN}Usage: ./run.sh [command]${NC}"
    echo
    echo "Commands:"
    echo "  lint               - Run static analysis (go vet) and format check"
    echo "  test               - Run all project tests"
    echo "  build              - Build the development binary at ./${BINARY_NAME}"
    echo "  release            - Build the release binary at ./dist/${BINARY_NAME}"
    echo "  all                - Run lint, test, and build (default)"
    echo "  help               - Show this help message"
}

function lint() {
    echo -e "${GREEN}==> Running static analysis (go vet)...${NC}"
    (cd "${SCRIPT_DIR}" && go vet ./...)

    echo -e "${GREEN}==> Formatting code (gofmt)...${NC}"
    (cd "${SCRIPT_DIR}" && gofmt -w .)

    echo -e "${GREEN}==> Checking formatting (gofmt)...${NC}"
    UNFORMATTED=$(cd "${SCRIPT_DIR}" && gofmt -l .)
    if [ -n "$UNFORMATTED" ]; then
        echo -e "${RED}The following files are not formatted:${NC}"
        echo "$UNFORMATTED"
        echo -e "${YELLOW}Run 'gofmt -w .' to fix.${NC}"
        exit 1
    fi
}

function test() {
    echo -e "${GREEN}==> Running tests...${NC}"
    (cd "${SCRIPT_DIR}" && go test -v ./...)
}

function build() {
    echo -e "${GREEN}==> Building development binary...${NC}"
    (cd "${SCRIPT_DIR}" && go build -o "${SCRIPT_DIR}/${BINARY_NAME}" ./cmd/statlite)
    echo -e "${GREEN}Done: ./${BINARY_NAME}${NC}"
}

function release() {
    RELEASE_DIR="${SCRIPT_DIR}/dist"
    echo -e "${GREEN}==> Building release binaries...${NC}"
    mkdir -p "${RELEASE_DIR}"

    local targets=(
        "linux/amd64:${BINARY_NAME}-linux-amd64"
        "linux/arm64:${BINARY_NAME}-linux-arm64"
        "darwin/amd64:${BINARY_NAME}-darwin-amd64"
        "darwin/arm64:${BINARY_NAME}-darwin-arm64"
    )

    for target in "${targets[@]}"; do
        goos="${target%%/*}"
        goarch="${target#*/}"
        goarch="${goarch%%:*}"
        out="${target##*:}"

        echo -e "${CYAN}  GOOS=${goos} GOARCH=${goarch} => dist/${out}${NC}"
        GOOS="${goos}" GOARCH="${goarch}" go build -ldflags="-s -w" -o "${RELEASE_DIR}/${out}" ./cmd/statlite
    done

    echo -e "${GREEN}Release builds complete: ./dist/${NC}"
    ls -lh "${RELEASE_DIR}"
}

COMMAND=${1:-all}

case $COMMAND in
    lint) lint ;;
    test) test ;;
    build) build ;;
    release) release ;;
    all) lint; test; build ;;
    help) help ;;
    *)
        echo -e "${RED}Unknown command: $COMMAND${NC}"
        help
        exit 1
        ;;
esac
