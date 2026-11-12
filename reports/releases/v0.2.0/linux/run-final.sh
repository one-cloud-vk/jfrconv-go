#!/bin/bash
set -eu
export PATH=/node/bin:/opt/java/openjdk/bin:/usr/bin:/usr/sbin:/bin:/sbin
export JAVA_HOME=/opt/java/openjdk
export JFR_REAL_INPUTS=/task/recordings/jdk.jfr:/task/recordings/ap41.jfr
{
  uname -a
  node --version
  java -version
  /task/bin/jfrconv --version
  cat /proc/sys/kernel/perf_event_paranoid
} > /task/results/test-environment.log 2>&1
set +e
cd /task/go-source/internal/jfr
/task/bin/jfr.test -test.v -test.count=1 -test.parallel=1 > /task/results/jfr-tests.log 2>&1
jfr_status=$?
cd /task/go-source/internal/convert
/task/bin/convert.test -test.v -test.count=1 -test.parallel=1 > /task/results/convert-tests.log 2>&1
convert_status=$?
cd /task/go-source/internal/cli
/task/bin/cli.test -test.v -test.count=1 -test.parallel=1 > /task/results/cli-tests.log 2>&1
cli_status=$?
cd /task/go-source/tools
python3 -m unittest test_otlp45 > /task/results/python-tests.log 2>&1
python_status=$?
cd /task
python3 /task/helpers/work/go-v45/run_public45.py --stage /task/public-native --output /task/results/public-native --native /task/bin/jfrconv --java /opt/java/openjdk/bin/java --reference-jar /task/reference/jfr-converter.jar --source-manifest /task/results/upstream-source.json --scope-class-dir /task > /task/results/public-runner.log 2>&1
public_status=$?
printf '{"jfr":%s,"convert":%s,"cli":%s,"python":%s,"public":%s}\n' "$jfr_status" "$convert_status" "$cli_status" "$python_status" "$public_status" > /task/results/exit-status.json
cat /task/results/exit-status.json
touch /task/control/tests-done
# Keep this task-owned compiler/Python environment only while the host reviews
# failures and runs any required pristine-Java/environment comparison.
while ! test -f /task/control/stop; do sleep 1; done
