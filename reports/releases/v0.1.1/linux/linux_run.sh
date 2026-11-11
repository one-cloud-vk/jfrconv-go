#!/bin/bash
set -eu
export PATH=/node/bin:/opt/java/openjdk/bin:/usr/bin:/usr/sbin:/bin:/sbin
export JAVA_HOME=/opt/java/openjdk
{
  uname -a
  node --version
  java -version
  /task/bin/jfrconv --version
} > /task/results/tool-versions.log 2>&1
cat /task/results/tool-versions.log
export JFR_REAL_INPUTS=/task/recordings/jdk.jfr:/task/recordings/ap41.jfr
set +e
cd /task/source/internal/jfr
/task/bin/jfr.test -test.v -test.count=1 -test.parallel=1 > /task/results/jfr-tests.log 2>&1
jfr_status=$?
cd /task/source/internal/convert
/task/bin/convert.test -test.v -test.count=1 -test.parallel=1 > /task/results/convert-tests.log 2>&1
convert_status=$?
cd /task/source/internal/cli
/task/bin/cli.test -test.v -test.count=1 -test.parallel=1 > /task/results/cli-tests.log 2>&1
cli_status=$?
mkdir -p /task/results/public/profiles
cd /public
JFRCONV_BINARY=/task/bin/jfrconv java -ea -Djava.library.path=build/test/lib -DthreadCount=1 -DretryCount=0 -DlogDir=/task/results/public/profiles -cp 'build/test.jar:build/jar/*:build/lib/*:test/deps/*:test/gen/*' one.profiler.test.Runner NativememTests.ldpreload > /task/results/public/tests.log 2>&1
public_status=$?
printf '{"jfr":%s,"convert":%s,"cli":%s,"public_ldpreload":%s}\n' "$jfr_status" "$convert_status" "$cli_status" "$public_status" > /task/results/exit-status.json
cat /task/results/exit-status.json
if test "$jfr_status" -ne 0 || test "$convert_status" -ne 0 || test "$cli_status" -ne 0 || test "$public_status" -ne 0; then exit 1; fi
