package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/testdb"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	amqp "github.com/rabbitmq/amqp091-go"
)

func readyzServer(t *testing.T, deps AuthDeps, publisher routing.Publisher) http.Handler {
	t.Helper()
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return New(config.Config{Port: "8080"}, store, deps, publisher, compile.NewRunner(deps, transit.DefaultBoardingWaitPolicy(), nil), logger.Discard(), nil, nil).Handler
}

func getReadyz(t *testing.T, h http.Handler) (int, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz: unmarshal %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func awaitReadyz(t *testing.T, h http.Handler, wantStatus int, within time.Duration) map[string]string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		status, body := getReadyz(t, h)
		if status == wantStatus {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("readyz: want %d within %s, still %d %v", wantStatus, within, status, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The shared throwaway Postgres cannot be stopped here: every other package's
// integration tests run against it at the same time. This test starts a
// container of its own from the same image and stops that one instead.
func TestIntegration_ReadyzFollowsPostgresAcrossARestart(t *testing.T) {
	testdb.URL(t)
	docker := os.Getenv("DOCKER")
	if docker == "" {
		docker = "docker"
	}
	if _, err := exec.LookPath(docker); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is required to run this test in CI: %v", docker, err)
		}
		t.Skipf("%s not found; skipping the Postgres restart test", docker)
	}
	image := os.Getenv("TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:16"
	}

	// A fixed host port rather than -P: a restarted container is not
	// guaranteed its old ephemeral port, and the pool would lose it.
	port := freePort(t)
	id, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	name := "sparks-effect-readyz-" + id[:8]
	dockerRun(t, docker, "run", "-d", "--name", name,
		"-e", "POSTGRES_PASSWORD=postgres",
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", port), image)
	t.Cleanup(func() { _ = exec.Command(docker, "rm", "-f", name).Run() })

	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	repo := connectWhenUp(t, dsn)
	t.Cleanup(repo.Close)
	h := readyzServer(t, repo, nil)

	if body := awaitReadyz(t, h, http.StatusOK, 5*time.Second); body["postgres"] != "ok" || body["amqp"] != "disabled" {
		t.Fatalf("readyz with Postgres up: want postgres=ok amqp=disabled, got %v", body)
	}

	dockerRun(t, docker, "stop", name)
	if body := awaitReadyz(t, h, http.StatusServiceUnavailable, 10*time.Second); body["postgres"] != "unavailable" {
		t.Fatalf("readyz with Postgres stopped: want postgres=unavailable, got %v", body)
	}

	// Same process, same pool: readiness must come back without a restart.
	dockerRun(t, docker, "start", name)
	if body := awaitReadyz(t, h, http.StatusOK, 60*time.Second); body["postgres"] != "ok" {
		t.Fatalf("readyz after Postgres restarted: want postgres=ok, got %v", body)
	}
}

func TestIntegration_ReadyzFailsWhenTheBrokerConnectionCloses(t *testing.T) {
	broker := os.Getenv("TEST_AMQP_URL")
	if broker == "" {
		broker = os.Getenv("AMQP_URL")
	}
	if broker == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_AMQP_URL (or AMQP_URL) must be set for queue integration tests in CI")
		}
		t.Skip("set TEST_AMQP_URL to run queue integration tests (see `make mq-up`)")
	}
	u, err := url.Parse(broker)
	if err != nil {
		t.Fatalf("parse TEST_AMQP_URL: %v", err)
	}

	// The shared broker cannot be stopped for the same reason as the shared
	// Postgres above. A proxy in front of it can: closing the proxy severs the
	// publisher's connection and refuses its redial, which is what the API sees
	// when the broker goes away.
	proxy := newTCPProxy(t, u.Host)
	u.Host = proxy.addr()

	queue := "test-readyz-" + t.Name()
	pub := routing.NewAMQPPublisher(u.String(), queue, logger.Discard())
	t.Cleanup(pub.Close)
	h := readyzServer(t, nil, pub)

	if body := awaitReadyz(t, h, http.StatusOK, 5*time.Second); body["amqp"] != "ok" || body["postgres"] != "disabled" {
		t.Fatalf("readyz with the broker up: want amqp=ok postgres=disabled, got %v", body)
	}
	t.Cleanup(func() { deleteQueue(broker, queue) })

	proxy.close()
	if body := awaitReadyz(t, h, http.StatusServiceUnavailable, 5*time.Second); body["amqp"] != "unavailable" {
		t.Fatalf("readyz with the broker connection closed: want amqp=unavailable, got %v", body)
	}
}

func dockerRun(t *testing.T, docker string, args ...string) {
	t.Helper()
	out, err := exec.Command(docker, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", docker, strings.Join(args, " "), err, out)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func connectWhenUp(t *testing.T, dsn string) *postgres.Repo {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		repo, err := postgres.Connect(ctx, dsn, 0)
		cancel()
		if err == nil {
			return repo
		}
		if time.Now().After(deadline) {
			t.Fatalf("Postgres container did not accept connections: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

type tcpProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	p := &tcpProxy{ln: ln, target: target}
	go p.serve()
	t.Cleanup(p.close)
	return p
}

func (p *tcpProxy) addr() string { return p.ln.Addr().String() }

func (p *tcpProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		upstream, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = client.Close()
			continue
		}
		if !p.track(client, upstream) {
			return
		}
		go pipe(client, upstream)
		go pipe(upstream, client)
	}
}

func (p *tcpProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		for _, c := range conns {
			_ = c.Close()
		}
		return false
	}
	p.conns = append(p.conns, conns...)
	return true
}

func (p *tcpProxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	_ = p.ln.Close()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
}

func deleteQueue(broker, queue string) {
	conn, err := amqp.Dial(broker)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return
	}
	defer func() { _ = ch.Close() }()
	_, _ = ch.QueueDelete(queue, false, false, false)
}
