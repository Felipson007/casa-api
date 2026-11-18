//go:build e2e

// Package e2e roda testes ponta a ponta contra o binário real da API (cmd/api), num banco Postgres
// criado do zero a cada execução. Nada é simulado: as migrations rodam no boot, o cadastro cria o
// schema do tenant, e as requisições passam por todo o middleware (sessão, CSRF, permissões).
//
// Como rodar (precisa de um Postgres acessível; o usuário precisa poder criar bancos):
//
//	E2E_POSTGRES_URL="postgres://postgres@localhost:5432/postgres?sslmode=disable" \
//	  go test -tags e2e -count=1 ./e2e/...
//
// Com E2E_COVERDIR definido, a API é compilada com -cover e grava a cobertura nesse diretório —
// é assim que scripts/coverage.sh soma o que os E2E exercitam à cobertura total.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// baseURL é o endereço da API em execução, preenchido por TestMain.
var baseURL string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	adminURL := os.Getenv("E2E_POSTGRES_URL")
	if adminURL == "" {
		fmt.Println("e2e: E2E_POSTGRES_URL não definido — pulando os testes E2E")
		return 0
	}

	work, err := os.MkdirTemp("", "casa-e2e-")
	if err != nil {
		return fail("criando diretório temporário", err)
	}
	defer os.RemoveAll(work)

	dbName := "casa_e2e_" + randomHex(6)
	if err := execAdmin(adminURL, "CREATE DATABASE "+dbName); err != nil {
		return fail("criando banco de teste", err)
	}
	defer func() {
		if err := execAdmin(adminURL, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
			fmt.Println("e2e: aviso: não consegui apagar o banco de teste:", err)
		}
	}()

	bin := filepath.Join(work, "api")
	if err := buildAPI(bin); err != nil {
		return fail("compilando cmd/api", err)
	}

	port, err := freePort()
	if err != nil {
		return fail("reservando porta", err)
	}
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+withDatabase(adminURL, dbName),
		fmt.Sprintf("PORT=%d", port),
		"APP_FRONTEND_ORIGIN="+baseURL,
		"APP_ENCRYPTION_KEY="+randomKey(),
		"TENANT_MASTER_KEY="+randomKey(),
		"APP_UPLOADS_DIR="+filepath.Join(work, "uploads"),
		"APP_BACKUPS_DIR="+filepath.Join(work, "backups"),
		"SIGNUP_ENABLED=true",
	)
	if dir := os.Getenv("E2E_COVERDIR"); dir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	logFile, err := os.Create(filepath.Join(work, "api.log"))
	if err != nil {
		return fail("criando log da API", err)
	}
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return fail("iniciando a API", err)
	}

	if err := waitReady(30 * time.Second); err != nil {
		_ = cmd.Process.Kill()
		dumpLog(logFile.Name())
		return fail("esperando a API ficar pronta", err)
	}

	code := m.Run()

	// SIGINT deixa a API encerrar normalmente — sem isso os dados de cobertura (-cover) não são gravados.
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
	}
	if code != 0 {
		dumpLog(logFile.Name())
	}
	return code
}

func buildAPI(out string) error {
	args := []string{"build", "-o", out}
	if os.Getenv("E2E_COVERDIR") != "" {
		// Sem -coverpkg de propósito: com ele o binário não grava os dados de cobertura ao sair. O
		// -cover sozinho já instrumenta todos os pacotes do módulo.
		args = append(args, "-cover")
	}
	args = append(args, "github.com/atlas/atlas-go/cmd/api")
	build := exec.Command("go", args...)
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	return build.Run()
}

func waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/auth/me")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("a API não respondeu em %s", timeout)
}

func execAdmin(adminURL, sql string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

// withDatabase troca o nome do banco na URL de conexão do administrador.
func withDatabase(adminURL, db string) string {
	u, err := url.Parse(adminURL)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + db
	return u.String()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func dumpLog(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fmt.Println("e2e: log da API:\n" + string(data))
}

func fail(step string, err error) int {
	fmt.Printf("e2e: falhou %s: %v\n", step, err)
	return 1
}
