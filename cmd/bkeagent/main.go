/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	bkeagentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	bkeagentctrl "gopkg.openfuyao.cn/cluster-api-provider-bke/controllers/bkeagent"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/option"
	v "gopkg.openfuyao.cn/cluster-api-provider-bke/version"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	// MinValidPort is the minimum valid port number
	MinValidPort = 1
	// MaxValidPort is the maximum valid port number
	MaxValidPort = 65535
	// ReservedPortThreshold is the threshold below which ports are considered reserved (require root)
	ReservedPortThreshold = 1024
)

// healthChecker holds the components needed for health checks
type healthChecker struct {
	client     client.Client
	restConfig *rest.Config
	ctx        context.Context
	manager    ctrl.Manager
	mu         sync.RWMutex
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(bkeagentv1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

type config struct {
	ntpServer   string
	ntpcron     string
	showVersion bool
	healthPort  string
	zapOpts     zap.Options
}

func parseFlags() config {
	cfg := config{
		ntpcron: "0 */1 * * *",
		zapOpts: zap.Options{Development: os.Getenv("DEBUG") == "true"},
	}
	flag.StringVar(&cfg.ntpServer, "ntpserver", "", "The ntp server address.")
	flag.StringVar(&cfg.ntpcron, "ntpcron", cfg.ntpcron, "The sync time cron.")
	flag.BoolVar(&cfg.showVersion, "v", false, "Show version and build information.")
	flag.StringVar(&cfg.healthPort, "health-port", "", "The port for HTTP health check endpoint.")
	flag.StringVar(&option.Platform, "platform", "", "The platform of the target cluster.")
	flag.StringVar(&option.Version, "version", "", "The version of the target cluster.")
	cfg.zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()
	return cfg
}

func newManager() (ctrl.Manager, error) {
	return ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&corev1.Secret{},
				},
			},
		},
	})
}

func setupController(mgr ctrl.Manager, j job.Job, ctx context.Context) error {
	hostName := utils.HostName()
	log.Infof("BKEAgent node hostName: %s", hostName)
	log.Infof("BKEAgent listening cluster: %s", mgr.GetConfig().Host)

	return (&bkeagentctrl.CommandReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Job:       j,
		NodeName:  hostName,
		Ctx:       ctx,
	}).SetupWithManager(mgr)
}

func (hc *healthChecker) checkHealth() (bool, string) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	select {
	case <-hc.ctx.Done():
		return false, "service is shutting down"
	default:
	}

	// Check if manager is ready
	if hc.manager == nil {
		return false, "manager not initialized"
	}

	// Check Kubernetes API connectivity by calling ServerVersion
	// This is the lightest check, equivalent to: kubectl version --short
	// It only verifies API server connectivity without requiring any resource permissions
	if hc.restConfig == nil {
		return false, "rest config not initialized"
	}

	_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientSet, err := kubernetes.NewForConfig(hc.restConfig)
	if err != nil {
		return false, fmt.Sprintf("failed to create Kubernetes client: %v", err)
	}

	_, err = clientSet.Discovery().ServerVersion()
	if err != nil {
		return false, fmt.Sprintf("failed to connect to management cluster API: %v", err)
	}

	return true, "ok"
}

// validatePort validates if the port number is within valid range
func validatePort(port int) error {
	if port < MinValidPort || port > MaxValidPort {
		return fmt.Errorf("port %d is out of valid range [%d-%d]", port, MinValidPort, MaxValidPort)
	}
	if port < ReservedPortThreshold {
		setupLog.Info("warning: port below 1024 may require root privileges", "port", port)
	}
	return nil
}

func isPortInUse(port int) (bool, error) {
	// First, try to bind to 0.0.0.0:port (most common case)
	// If 0.0.0.0:port is in use, usually 127.0.0.1:port cannot be bound either
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr != nil && opErr.Err != nil {
			errStr := opErr.Err.Error()
			if containsAny(errStr, []string{
				"address already in use",
				"bind: address already in use",
				"Only one usage of each socket address",
			}) {
				return true, nil
			}
		}
		// If it's not an "address already in use" error, try 127.0.0.1
		// Some systems might allow binding to 127.0.0.1 even if 0.0.0.0 is in use
		addr = fmt.Sprintf("127.0.0.1:%d", port)
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			var opErr2 *net.OpError
			if errors.As(err, &opErr2) && opErr2 != nil && opErr2.Err != nil {
				errStr := opErr2.Err.Error()
				if containsAny(errStr, []string{
					"address already in use",
					"bind: address already in use",
					"Only one usage of each socket address",
				}) {
					return true, nil
				}
			}
			return false, fmt.Errorf("failed to check port availability: %v", err)
		}
	}

	// Successfully created listener means port is available
	if err = listener.Close(); err != nil {
		return false, fmt.Errorf("failed to close test listener: %v", err)
	}

	// Additional check: try to connect to the port to see if something is listening
	// This helps catch cases where the port might be in TIME_WAIT state or other edge cases
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err == nil {
		// Connection successful means something is listening on the port
		err = conn.Close()
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// startHealthServer starts an HTTP health check server on 127.0.0.1:port
func startHealthServer(port int, hc *healthChecker) error {
	if port <= 0 {
		return nil
	}

	// Validate port range
	if err := validatePort(port); err != nil {
		return fmt.Errorf("invalid port: %v", err)
	}

	// Check if port is already in use
	inUse, err := isPortInUse(port)
	if err != nil {
		return fmt.Errorf("failed to check port availability: %v", err)
	}
	if inUse {
		return fmt.Errorf("port %d is already in use", port)
	}

	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		healthy, message := hc.checkHealth()
		if healthy {
			w.WriteHeader(http.StatusOK)
			_, err = w.Write([]byte(message))
			if err != nil {
				return
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, err = w.Write([]byte(message))
			if err != nil {
				return
			}
		}
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	// Start server in a goroutine
	go func() {
		setupLog.Info("starting health check server", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			setupLog.Error(err, "health check server error")
		}
	}()

	// Graceful shutdown
	go func() {
		<-hc.ctx.Done()
		setupLog.Info("shutting down health check server")
		if err := server.Shutdown(context.Background()); err != nil {
			setupLog.Error(err, "error shutting down health check server")
		}
	}()

	return nil
}

func run(cfg config) {
	//	In the target cluster, check and install the CRD
	if err := enableCrdHasInstalled(); err != nil {
		log.Errorf("The CRD cannot be installed in the target cluster, %s", err.Error())
		return
	}

	ctx := ctrl.SetupSignalHandler()

	// Initialize health checker
	hc := &healthChecker{
		ctx: ctx,
	}

	// Start health check server if enabled
	if cfg.healthPort == "" {
		setupLog.Info("health check server disabled (no port specified)")
	} else {
		port, err := strconv.Atoi(cfg.healthPort)
		if err != nil {
			setupLog.Error(err, "invalid health port", "port", cfg.healthPort)
			os.Exit(1)
		}
		if port == 0 {
			setupLog.Info("health check server disabled (port is 0)")
		} else {
			if err = startHealthServer(port, hc); err != nil {
				setupLog.Error(err, "unable to start health check server", "port", port)
				os.Exit(1)
			}
		}
	}

	mgr, err := newManager()
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Update health checker with manager, client and rest config
	hc.mu.Lock()
	hc.manager = mgr
	hc.client = mgr.GetClient()
	hc.restConfig = mgr.GetConfig()
	hc.mu.Unlock()

	j, err := job.NewJob(mgr.GetClient())
	if err != nil {
		log.Fatal(err)
	}

	if err := setupController(mgr, j, ctx); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Command")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func main() {
	cfg := parseFlags()

	if cfg.showVersion {
		v.PrintVersion()
		os.Exit(0)
	}

	v.LogPrintVersion()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&cfg.zapOpts)))

	run(cfg)
}
