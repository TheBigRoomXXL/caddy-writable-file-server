#!/bin/sh

OUTPUT=0
API="http://localhost:8888"
CADDY_PATH=${CADDY_PATH:-./caddy}

# ╔═══════════════════════════════════════════╗
# ║                  SETUP                    ║
# ╚═══════════════════════════════════════════╝
# Check it is built
if [ ! -f "$CADDY_PATH" ]; then
    printf "Error: caddy not found at '$CADDY_PATH'\n" >&2
    exit 1
fi

# Start caddy in the backgound
./caddy run > tests/caddy-stdout.log 2> tests/caddy-stderr.log &
CADDY_PID=$!

# Ensure everything is cleaned on exit
cleanup() {
  rm -rf tests/root/*
  kill $CADDY_PID
}
trap cleanup EXIT

# Clean the test directories
rm -rf tests/root/*
mkdir -p tests/results/

# Load the test config into caddy
curl http://localhost:2019/load -H "Content-Type: application/json" -d @tests/assets/config.json

# Prepare assert func
assert_success() {
  tree tests/root/ > tests/results/$1 
  assertion=$( diff -u  tests/expected/$1 tests/results/$1 )
  if [ -n "$assertion" ]; then
    printf "Test %s failed:\n" "$1"
    printf '%s\n' "$assertion"
    OUTPUT=1
  fi
  
  # Cleanup after test
  rm -rf tests/root/*
}


# ╔═══════════════════════════════════════════╗
# ║                  TESTS                    ║
# ╚═══════════════════════════════════════════╝
# TEST 1: Upload a file
curl -XPUT --data-binary @tests/assets/test.txt ${API}/test.txt
assert_success "01-upload-file"

# TEST 2: Upload a tar
curl -XPUT --data-binary @tests/assets/test.tar -H "Content-Type: application/x-tar" ${API}/
assert_success "02-upload-tar"

# TEST 3: Upload a tar.gz
curl -XPUT --data-binary @tests/assets/test.tar.gz -H "Content-Type: application/x-tar+gzip" ${API}/
assert_success "03-upload-tar-gz"

# TEST 4: Delete file
cp tests/assets/test.txt tests/root/index.txt
curl -XDELETE ${API}/index.txt
assert_success "04-delete-file"

# TEST 5: Delete directory
tar -xf tests/assets/test.tar  -C tests/root/
curl -XDELETE ${API}/tested/with-file/
assert_success "05-delete-directory"

if [ "$OUTPUT" -eq 0 ]; then
  printf "Tests succeded.\n"
else
  printf "\nTests failed.\n"
fi
exit $OUTPUT
