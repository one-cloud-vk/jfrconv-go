#!/usr/bin/sh
# Run only after the frozen host snapshots and Linux Go executables are ready.
set -eu
export PATH=/usr/bin:/usr/sbin:/bin:/sbin:/opt/java/openjdk/bin
export JAVA_HOME=/opt/java/openjdk
export DEBIAN_FRONTEND=noninteractive
results=/work/final-native-results
mkdir -p "$results"
dpkg --configure -a || true
apt-get update
apt-get install -f -y --no-install-recommends
apt-get install -y --no-install-recommends build-essential python3 rustc cargo nodejs
{
  uname -a
  rustc --version
  cargo --version
  node --version
  java -version
  /work/final-go-bin/jfrconv --version
} > "$results/tool-versions.log" 2>&1
cat "$results/tool-versions.log"
export CARGO_HOME=/work/cargo
export CARGO_TARGET_DIR=/work/linux-target
cd /work/final-rust-source
cargo build --release --locked --offline
sha256sum /work/linux-target/release/jfrconv /work/final-go-bin/jfrconv > "$results/linux-binaries.sha256"
set +e
cargo test --release --locked --offline -- --nocapture --test-threads=1 > "$results/cargo-tests.log" 2>&1
cargo_status=$?
cat "$results/cargo-tests.log"
cd /work/final-go-source/internal/jfr
export JFR_REAL_INPUTS=/work/final-real-inputs/jdk.jfr:/work/final-real-inputs/ap41.jfr
/work/final-go-bin/jfr.test -test.v -test.count=1 -test.parallel=1 > "$results/go-jfr-tests.log" 2>&1
go_jfr_status=$?
cat "$results/go-jfr-tests.log"
cd /work/final-go-source/internal/convert
/work/final-go-bin/convert.test -test.v -test.count=1 -test.parallel=1 > "$results/go-convert-tests.log" 2>&1
go_convert_status=$?
cat "$results/go-convert-tests.log"
cd /work/final-go-source/internal/cli
/work/final-go-bin/cli.test -test.v -test.count=1 -test.parallel=1 > "$results/go-cli-tests.log" 2>&1
go_cli_status=$?
cat "$results/go-cli-tests.log"
python3 /work/final-rust-source/tools/upstream_tests.py run --dest /work/v41-java --suite v4.1 --test NativememTests.ldpreload --no-build
java_status=$?
JFRCONV_BINARY=/work/linux-target/release/jfrconv python3 /work/final-rust-source/tools/upstream_tests.py run --dest /work/v41-rust --suite v4.1 --test NativememTests.ldpreload --no-build
rust_public_status=$?
JFRCONV_BINARY=/work/final-go-bin/jfrconv python3 /work/final-go-source/tools/upstream_tests.py run --dest /work/v41-go --suite v4.1 --test NativememTests.ldpreload
go_public_status=$?
printf '{"cargo":%s,"go_jfr":%s,"go_convert":%s,"go_cli":%s,"java_public":%s,"rust_public":%s,"go_public":%s}\n' "$cargo_status" "$go_jfr_status" "$go_convert_status" "$go_cli_status" "$java_status" "$rust_public_status" "$go_public_status" > "$results/exit-status.json"
cat "$results/exit-status.json"
if test "$cargo_status" -ne 0 || test "$go_jfr_status" -ne 0 || test "$go_convert_status" -ne 0 || test "$go_cli_status" -ne 0 || test "$java_status" -ne 0 || test "$rust_public_status" -ne 0 || test "$go_public_status" -ne 0; then
  exit 1
fi
printf 'Frozen Rust and Go Linux validation passed under default Docker security.\n'
