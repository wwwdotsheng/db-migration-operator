//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// smokeKubectl bounds each command so a stalled API request cannot hang the suite.
func smokeKubectl(input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kubectl %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func postgresMigrationSucceeds() {
	testNamespace := "migration-smoke-" + uuid.NewString()
	_, err := smokeKubectl("", "create", "namespace", testNamespace)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if CurrentSpecReport().Failed() {
			for _, args := range [][]string{
				{"get", "schemamigrations", "-o", "yaml"},
				{"describe", "jobs"},
				{"get", "events", "--sort-by=.lastTimestamp"},
				{"logs", "-l", "shadow-of=add-remark", "--all-containers=true", "--tail=100"},
			} {
				out, _ := smokeKubectl("", append(args, "-n", testNamespace)...)
				fmt.Fprintln(GinkgoWriter, out)
			}
		}
		_, cleanupErr := smokeKubectl("", "delete", "namespace", testNamespace, "--wait=false")
		Expect(cleanupErr).NotTo(HaveOccurred())
	})

	By("starting a disposable PostgreSQL 16 database")
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: database-password
type: Opaque
stringData:
  password: %s
---
apiVersion: v1
kind: Pod
metadata:
  name: database
  labels:
    app: migration-smoke-database
spec:
  containers:
    - name: postgres
      image: postgres:16-alpine
      env:
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: database-password
              key: password
        - name: POSTGRES_DB
          value: migration_test
      readinessProbe:
        exec:
          command: [pg_isready, -h, 127.0.0.1, -U, postgres, -d, migration_test]
        periodSeconds: 2
      volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
  volumes:
    - name: data
      emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: database
spec:
  selector:
    app: migration-smoke-database
  ports:
    - port: 5432
      targetPort: 5432
`, uuid.NewString())
	_, err = smokeKubectl(manifest, "create", "-n", testNamespace, "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() (string, error) {
		return smokeKubectl("", "get", "pod", "database", "-n", testNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
	}, 5*time.Minute, 2*time.Second).Should(Equal("True"))

	By("creating the original table and submitting a migration")
	_, err = smokeKubectl("", "exec", "-n", testNamespace, "database", "--",
		"psql", "-U", "postgres", "-d", "migration_test", "-v", "ON_ERROR_STOP=1",
		"-c", "CREATE TABLE orders (id bigint PRIMARY KEY)")
	Expect(err).NotTo(HaveOccurred())
	migration := fmt.Sprintf(`apiVersion: db.migration.dev/v1
kind: SchemaMigration
metadata:
  name: add-remark
spec:
  name: add-remark
  changeType: Expand
  driver: postgres
  driverConfig:
    host: database.%s.svc.cluster.local
    port: 5432
    user: postgres
    database: migration_test
    schema: public
    sslMode: disable
    passwordSecretRef:
      name: database-password
      key: password
  version: 1
  applySql:
    - "ALTER TABLE orders ADD COLUMN remark varchar(100)"
`, testNamespace)
	_, err = smokeKubectl(migration, "create", "-n", testNamespace, "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() (string, error) {
		phase, err := smokeKubectl("", "get", "schemamigration", "add-remark", "-n", testNamespace,
			"-o", "jsonpath={.status.phase}")
		if err == nil && phase == "Failed" {
			StopTrying("migration entered Failed; see CR and shadow Job diagnostics").Now()
		}
		return phase, err
	}, 5*time.Minute, 2*time.Second).Should(Equal("Done"))

	By("checking shadow validation, the real column and the database ledger")
	condition, err := smokeKubectl("", "get", "schemamigration", "add-remark", "-n", testNamespace,
		"-o", `jsonpath={.status.conditions[?(@.type=="ShadowValidated")].status}`)
	Expect(err).NotTo(HaveOccurred())
	Expect(condition).To(Equal("True"))
	succeeded, err := smokeKubectl("", "get", "jobs", "-n", testNamespace,
		"-l", "shadow-of=add-remark", "-o", "jsonpath={.items[*].status.succeeded}")
	Expect(err).NotTo(HaveOccurred())
	Expect(succeeded).To(Equal("1"))
	query := `SELECT data_type || ':' || is_nullable FROM information_schema.columns
WHERE table_schema='public' AND table_name='orders' AND column_name='remark';
SELECT status FROM schema_migrations WHERE version=1 AND checksum IS NOT NULL;`
	result, err := smokeKubectl("", "exec", "-n", testNamespace, "database", "--",
		"psql", "-U", "postgres", "-d", "migration_test", "-v", "ON_ERROR_STOP=1", "-At", "-c", query)
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.ReplaceAll(result, "\r\n", "\n")).To(Equal("character varying:YES\nDone"))
}
