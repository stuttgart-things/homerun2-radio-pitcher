// Dagger CI module for homerun2-radio-pitcher
//
// Provides build, lint, test, image build, and security scanning functions.
// Delegates to external stuttgart-things Dagger modules where possible.

package main

import (
	"context"
	"dagger/dagger/internal/dagger"
	"fmt"
)

type Dagger struct{}

// Lint runs golangci-lint on the source code
func (m *Dagger) Lint(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="500s"
	timeout string,
) *dagger.Container {
	return dag.Go().Lint(src, dagger.GoLintOpts{
		Timeout: timeout,
	})
}

// Govulncheck reports vulnerabilities from the Go vulnerability database that
// are actually reachable from this module's code. It complements the Trivy
// image scan: Trivy covers the base image and OS packages, this covers the Go
// module graph, and each finds things the other misses.
//
// Report-only by default (failOnVuln=false) so it can be adopted without
// breaking the build; set failOnVuln=true to turn it into a gate.
func (m *Dagger) Govulncheck(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="latest"
	govulncheckVersion string,
	// +optional
	// +default=false
	failOnVuln bool,
) *dagger.File {
	const reportPath = "/tmp/govulncheck-report.txt"

	// govulncheck exits 3 when it finds something. Capture the report first and
	// always echo it, so the run is readable whether or not it is a hard gate.
	script := fmt.Sprintf(`
set -u
govulncheck ./... > %[1]s 2>&1
code=$?
cat %[1]s
if [ "%[2]t" = "true" ] && [ "$code" -ne 0 ]; then
  echo "govulncheck found vulnerabilities (exit $code)" >&2
  exit "$code"
fi
exit 0
`, reportPath, failOnVuln)

	return dag.Container().
		From("golang:"+goVersion).
		// The golang image pins GOTOOLCHAIN=local; govulncheck and this module
		// may both want a newer toolchain than the image ships.
		WithEnvVariable("GOTOOLCHAIN", "auto").
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@" + govulncheckVersion}).
		WithExec([]string{"sh", "-c", script}).
		File(reportPath)
}

// Test runs the Go test suite with redis-stack bound. REDIS_ADDR switches on
// the integration test in internal/pitcher, which pitches into a stream and
// reads the message back. The call fails when the suite fails.
func (m *Dagger) Test(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="./..."
	testPath string,
) (string, error) {
	redisService := dag.Homerun().RedisService(dagger.HomerunRedisServiceOpts{
		Version:  "7.2.0-v18",
		Password: "",
	})

	return dag.Container().
		From("golang:"+goVersion).
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("gomod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("gobuild")).
		WithServiceBinding("redis", redisService).
		WithEnvVariable("REDIS_ADDR", "redis").
		WithEnvVariable("REDIS_PORT", "6379").
		WithExec([]string{"go", "test", "-v", "-cover", testPath}).
		Stdout(ctx)
}

// Build compiles the Go binary
func (m *Dagger) Build(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="main"
	binName string,
	// +optional
	// +default=""
	ldflags string,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="linux"
	os string,
	// +optional
	// +default="amd64"
	arch string,
) *dagger.Directory {
	return dag.Go().BuildBinary(src, dagger.GoBuildBinaryOpts{
		GoVersion:  goVersion,
		Os:         os,
		Arch:       arch,
		BinName:    binName,
		Ldflags:    ldflags,
		GoMainFile: "main.go",
	})
}

// BuildImage builds a container image using ko and optionally pushes it
func (m *Dagger) BuildImage(
	ctx context.Context,
	src *dagger.Directory,
	// +optional
	// +default="ko.local/homerun2-radio-pitcher"
	repo string,
	// +optional
	// +default="false"
	push string,
) (string, error) {
	return dag.Go().KoBuild(ctx, src, dagger.GoKoBuildOpts{
		Repo: repo,
		Push: push,
	})
}

// ScanImage scans a container image for vulnerabilities using Trivy
func (m *Dagger) ScanImage(
	ctx context.Context,
	imageRef string,
	// +optional
	// +default="HIGH,CRITICAL"
	severity string,
) *dagger.File {
	return dag.Trivy().ScanImage(imageRef, dagger.TrivyScanImageOpts{
		Severity: severity,
	})
}

// BuildAndTestBinary builds the binary and runs it against redis-stack: the
// service starts, answers /health and /ready, and `once time-check` puts a
// message on the ambient stream.
func (m *Dagger) BuildAndTestBinary(
	ctx context.Context,
	source *dagger.Directory,
	// +optional
	// +default="1.26.11"
	goVersion string,
	// +optional
	// +default="linux"
	os string,
	// +optional
	// +default="amd64"
	arch string,
	// +optional
	// +default="main.go"
	goMainFile string,
	// +optional
	// +default="main"
	binName string,
	// +optional
	// +default=""
	ldflags string,
	// +optional
	// +default="."
	packageName string,
	// +optional
	// +default="8080"
	port int,
) (*dagger.File, error) {

	binDir := dag.Go().BuildBinary(
		source,
		dagger.GoBuildBinaryOpts{
			GoVersion:   goVersion,
			Os:          os,
			Arch:        arch,
			GoMainFile:  goMainFile,
			BinName:     binName,
			Ldflags:     ldflags,
			PackageName: packageName,
		})

	redisService := dag.Homerun().RedisService(dagger.HomerunRedisServiceOpts{
		Version:  "7.2.0-v18",
		Password: "",
	})

	testCmd := fmt.Sprintf(`
(
set -e

echo "=== validate the example schedule ==="
./%[1]s validate

echo ""
echo "=== start the service ==="
./%[1]s serve &
BIN_PID=$!
sleep 3

echo ""
echo "=== /health ==="
curl -fsS http://localhost:%[2]d/health || { kill $BIN_PID; exit 1; }

echo ""
echo "=== /ready (schedule loaded, redis reachable) ==="
curl -fsS http://localhost:%[2]d/ready || { kill $BIN_PID; exit 1; }

echo ""
echo "=== once time-check pitches into the ambient stream ==="
./%[1]s once time-check
LEN=$(redis-cli -h redis XLEN ambient)
echo "ambient stream length: $LEN"
[ "$LEN" -ge 1 ] || { echo "nothing on the ambient stream"; kill $BIN_PID; exit 1; }
ID=$(redis-cli -h redis --raw XREVRANGE ambient + - COUNT 1 | sed -n 3p)
DOC=$(redis-cli -h redis --raw JSON.GET "$ID")
echo "$DOC"
echo "$DOC" | grep -q '"system":"radio"' || { echo "system is not radio"; kill $BIN_PID; exit 1; }
echo "$DOC" | grep -q '"tags":"segment=time-check"' || { echo "tags lack the segment"; kill $BIN_PID; exit 1; }
MSGS=$(redis-cli -h redis XLEN messages)
[ "$MSGS" = "0" ] || { echo "the messages stream was written to"; kill $BIN_PID; exit 1; }

echo ""
echo "=== All tests passed! ==="
kill $BIN_PID 2>/dev/null || true
) > /app/test-output.log 2>&1
rc=$?
cat /app/test-output.log
exit $rc
`, binName, port)

	result := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "curl", "redis"}).
		WithDirectory("/app", binDir).
		WithFile("/app/schedule.yaml", source.File("examples/schedule.yaml")).
		WithWorkdir("/app").
		WithServiceBinding("redis", redisService).
		WithEnvVariable("REDIS_ADDR", "redis").
		WithEnvVariable("REDIS_PORT", "6379").
		WithEnvVariable("RADIO_SCHEDULE_PATH", "/app/schedule.yaml").
		WithEnvVariable("LOG_FORMAT", "text").
		WithExec([]string{"sh", "-c", testCmd})

	_, err := result.Sync(ctx)
	if err != nil {
		testLog := result.File("/app/test-output.log")
		return testLog, fmt.Errorf("tests failed - check test-output.log for details: %w", err)
	}

	return result.File("/app/test-output.log"), nil
}
