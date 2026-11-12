#!/bin/bash
set -eu
export PATH=/opt/java/openjdk/bin:/usr/bin:/usr/sbin:/bin:/sbin
export JAVA_HOME=/opt/java/openjdk
export DEBIAN_FRONTEND=noninteractive
apt-get -o APT::Sandbox::User=root -o APT::Update::Error-Mode=any -o Acquire::Retries=0 -o Acquire::http::Timeout=15 update
apt-get -o APT::Sandbox::User=root -o Acquire::Retries=0 -o Acquire::http::Timeout=15 install -y --no-install-recommends build-essential python3
{
  uname -a
  gcc --version
  g++ --version
  python3 --version
  java -version
} > /task/results/build-tool-versions.log 2>&1
cd /task/public-native
make -j2 build-test-java > /task/results/public-build.log 2>&1
javac -cp 'build/test.jar:build/jar/*:build/lib/*:test/deps/*:test/gen/*' -d /task /task/PublicScope.java
sha256sum build/lib/libasyncProfiler.so build/jar/jfr-converter.jar build/test.jar > /task/results/public-build.sha256
printf 'Pinned async-profiler4.5 Linux profiler/test build completed.\n'
touch /task/control/build-ready
while ! test -f /task/control/go-ready; do
  if test -f /task/control/stop; then exit 130; fi
  sleep 1
done
exec /bin/bash /task/run-final.sh
