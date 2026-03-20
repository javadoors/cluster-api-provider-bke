/******************************************************************
 * Copyright (c) 2024 Bocloud Technologies Co., Ltd.
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
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	controlv1beta1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	clusterexpv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	commonutils "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils"
	capbkecontrollers "gopkg.openfuyao.cn/cluster-api-provider-bke/controllers/capbke"
	bkemetrics "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
	scriptshelper "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/scriptshelper"
	v "gopkg.openfuyao.cn/cluster-api-provider-bke/version"
	capbkewebhooks "gopkg.openfuyao.cn/cluster-api-provider-bke/webhooks/capbke"
)

const (
	// EventBroadcasterBurstSize is the burst size for the event broadcaster
	EventBroadcasterBurstSize = 10000
	// EventBroadcasterLRUCacheSize is the LRU cache size for the event broadcaster
	EventBroadcasterLRUCacheSize = 4096 * 2
	// EventBroadcasterMaxIntervalInSeconds is the max interval in seconds for the event broadcaster
	EventBroadcasterMaxIntervalInSeconds = 30
)

const (
	// FastSlowRateLimiterSlowDuration is the slow duration for the fast-slow rate limiter (60 seconds)
	FastSlowRateLimiterSlowDuration = 60 * time.Second
	// FastSlowRateLimiterFastDuration is the fast duration for the fast-slow rate limiter (2 seconds)
	FastSlowRateLimiterFastDuration = 2 * time.Second
	// FastSlowRateLimiterRetryCount is the retry count for the fast-slow rate limiter
	FastSlowRateLimiterRetryCount = 10
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	//设置时区为上海
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		time.Local = loc
		setupLog.Info("Set timezone to Asia/Shanghai")
	}

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(clusterexpv1.AddToScheme(scheme))
	utilruntime.Must(agentv1beta1.AddToScheme(scheme))
	utilruntime.Must(controlv1beta1.AddToScheme(scheme))
	utilruntime.Must(bootstrapv1.AddToScheme(scheme))

	utilruntime.Must(bkev1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	setupLog.Info("--------------Starting the BKE Cluster API Provider---------------")
	printVersionInfo()
	setupLog.Info("------------------------------------------------------------------")

	printManifestsBuildInfo()

	mgr, tracker := createManager()
	ctx := ctrl.SetupSignalHandler()

	setupControllers(ctx, mgr, tracker)
	setupWebhooks(mgr)

	// Setup health checks based on probe scheme
	if config.ProbeScheme == "https" {
		// Start independent HTTPS health check server
		if err := startHTTPSHealthServer(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to start HTTPS health check server")
			os.Exit(1)
		}
	} else {
		// Use default HTTP health check server
		setupHealthChecks(mgr)
	}

	registerMetric(mgr)
	registerProfiler(mgr)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// printVersionInfo prints version information
func printVersionInfo() {
	setupLog.Info(fmt.Sprintf("🤯 Version: %s", v.Version))
	setupLog.Info(fmt.Sprintf("🤔 GitCommitId: %s ", v.GitCommitID))
	setupLog.Info(fmt.Sprintf("👉 Architecture: %s", v.Architecture))
	setupLog.Info(fmt.Sprintf("⏲ BuildTime: %s", v.BuildTime))
}

// printManifestsBuildInfo prints manifests build information
func printManifestsBuildInfo() {
	if manifestInfo, err := commonutils.GetManifestsBuildInfo(); err == nil {
		for _, v := range manifestInfo {
			setupLog.Info(v)
		}
	} else {
		setupLog.Info("(ignore)Failed to get manifests build info: %v", err)
	}
}

// createManager creates and configures the manager
func createManager() (ctrl.Manager, *remote.ClusterCacheTracker) {
	config.ConfigurationFlag()

	opts := zap.Options{
		Development: os.Getenv("DEBUG") == "true",
		Encoder:     log.Encoder,
	}
	opts.BindFlags(flag.CommandLine)

	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Configure health probe based on scheme
	healthProbeAddr := config.ProbeAddr
	if config.ProbeScheme == "https" {
		// Disable default HTTP health check server when using HTTPS
		healthProbeAddr = "0"
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     config.MetricsAddr,
		Port:                   config.WebhookPort,
		Host:                   config.WebhookHost,
		HealthProbeBindAddress: healthProbeAddr,
		LeaderElection:         config.EnableLeaderElection,
		LeaderElectionID:       "e2b5373a.bocloud.com",
		CertDir:                config.WebhookCertDir,
		EventBroadcaster: record.NewBroadcasterWithCorrelatorOptions(record.CorrelatorOptions{
			KeyFunc:              EventAggregatorByMessageFunc,
			BurstSize:            EventBroadcasterBurstSize,
			LRUCacheSize:         EventBroadcasterLRUCacheSize,
			MaxIntervalInSeconds: EventBroadcasterMaxIntervalInSeconds,
		}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := scriptshelper.CreateScriptsConfigMaps(mgr.GetClient()); err != nil {
		setupLog.Error(err, "unable to create scripts configmaps")
	}

	tracker, err := remote.NewClusterCacheTracker(
		mgr,
		remote.ClusterCacheTrackerOptions{
			Indexes: remote.DefaultIndexes,
		},
	)
	if err != nil {
		setupLog.Error(err, "unable to create cluster cache tracker")
		os.Exit(1)
	}

	return mgr, tracker
}

// setupControllers sets up the controllers
func setupControllers(ctx context.Context, mgr ctrl.Manager, tracker *remote.ClusterCacheTracker) {
	if err := (&capbkecontrollers.BKEClusterReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("bke-cluster"),
		RestConfig: mgr.GetConfig(),
		Tracker:    tracker,
	}).SetupWithManager(ctx, mgr, concurrency(config.BkeClusterConcurrency)); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BKECluster")
		os.Exit(1)
	}
	if err := (&capbkecontrollers.BKEMachineReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("bke-machine"),
	}).SetupWithManager(mgr, concurrency(config.BkeMachineConcurrency)); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BKEMachine")
		os.Exit(1)
	}
}

// setupWebhooks sets up the webhooks
func setupWebhooks(mgr ctrl.Manager) {
	if err := (&capbkewebhooks.BKECluster{
		Client:      mgr.GetClient(),
		NodeFetcher: nodeutil.NewNodeFetcher(mgr.GetClient()),
		APIReader:   mgr.GetAPIReader(),
	}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "BKECluster")
		os.Exit(1)
	}
}

// setupHealthChecks sets up health and ready checks for HTTP probe
func setupHealthChecks(mgr ctrl.Manager) {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}
}

// validateTLSCertificates checks if TLS certificate files exist
func validateTLSCertificates(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return fmt.Errorf("TLS certificate not found at %s", certPath)
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		return fmt.Errorf("TLS key not found at %s", keyPath)
	}
	return nil
}

// loadTLSConfig loads TLS certificate and creates TLS configuration
func loadTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// setupHealthEndpoints sets up health check endpoints on the mux
func setupHealthEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("ok"))
		if err != nil {
			return
		}
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("ok"))
		if err != nil {
			return
		}
	})
}

// startHTTPSListener starts the HTTPS server listener in a goroutine
func startHTTPSListener(server *http.Server, tlsConfig *tls.Config, certPath, keyPath string) {
	setupLog.Info("starting HTTPS health check server", "address", server.Addr, "cert", certPath, "key", keyPath)
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		setupLog.Error(err, "failed to listen on HTTPS health check port")
		return
	}
	tlsListener := tls.NewListener(ln, tlsConfig)
	if err = server.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		setupLog.Error(err, "HTTPS health check server error")
	}
}

// setupGracefulShutdown sets up graceful shutdown for the HTTPS server
func setupGracefulShutdown(ctx context.Context, server *http.Server) {
	go func() {
		<-ctx.Done()
		setupLog.Info("shutting down HTTPS health check server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			setupLog.Error(err, "error shutting down HTTPS health check server")
		}
	}()
}

// startHTTPSHealthServer starts an independent HTTPS health check server
// using TLS certificate from /etc/kubernetes/tls-server.crt and tls-server.key
// Note: Uses port 9444 by default to avoid conflict with webhook server (port 9443)
func startHTTPSHealthServer(ctx context.Context, mgr ctrl.Manager) error {
	const (
		certPath = "/etc/kubernetes/tls-server.crt"
		keyPath  = "/etc/kubernetes/tls-server.key"
	)
	port := config.ProbePort
	if port <= 0 {
		port = 9444 // Default port for HTTPS health check server
	}

	if err := validateTLSCertificates(certPath, keyPath); err != nil {
		return err
	}

	tlsConfig, err := loadTLSConfig(certPath, keyPath)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	setupHealthEndpoints(mux)

	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", port),
		Handler:   mux,
		TLSConfig: tlsConfig,
	}

	go startHTTPSListener(server, tlsConfig, certPath, keyPath)
	setupGracefulShutdown(ctx, server)

	return nil
}

// EventAggregatorByMessageFunc aggregates events by exact match on event.Source, event.InvolvedObject, event.Type,
// event.Reason, event.ReportingController and event.ReportingInstance
func EventAggregatorByMessageFunc(event *corev1.Event) (string, string) {
	return strings.Join([]string{
		event.Source.Component,
		event.Source.Host,
		event.InvolvedObject.Kind,
		event.InvolvedObject.Namespace,
		event.InvolvedObject.Name,
		string(event.InvolvedObject.UID),
		event.InvolvedObject.APIVersion,
		event.Type,
		event.Message,
		event.ReportingController,
		event.ReportingInstance,
	},
		""), event.Message
}

func concurrency(c int) controller.Options {
	recoverPanic := true
	return controller.Options{
		MaxConcurrentReconciles: c,
		RecoverPanic:            &recoverPanic,
		RateLimiter: workqueue.NewItemFastSlowRateLimiter(
			FastSlowRateLimiterFastDuration,
			FastSlowRateLimiterSlowDuration,
			FastSlowRateLimiterRetryCount),
	}
}

func registerMetric(mgr ctrl.Manager) {
	if config.MetricsAddr == "0" {
		return
	}

	if err := mgr.AddMetricsExtraHandler("/export", bkemetrics.MetricRegister.HttpExportFunc()); err != nil {
		setupLog.Error(err, "unable to set up extra metrics handler")
		os.Exit(1)
	}
	if err := mgr.AddMetricsExtraHandler("/cluster", bkemetrics.MetricRegister.HttpClusterFunc()); err != nil {
		setupLog.Error(err, "unable to set up extra metrics handler")
		os.Exit(1)
	}
}

func registerProfiler(m ctrl.Manager) {
	if os.Getenv("DEBUG") != "true" && config.MetricsAddr == "0" {
		return
	}

	endpoints := map[string]http.HandlerFunc{
		"/debug/pprof/":        pprof.Index,
		"/debug/pprof/cmdline": pprof.Cmdline,
		"/debug/pprof/profile": pprof.Profile,
		"/debug/pprof/symbol":  pprof.Symbol,
		"/debug/pprof/trace":   pprof.Trace,
	}

	for path, handler := range endpoints {
		err := m.AddMetricsExtraHandler(path, handler)
		if err != nil {
			setupLog.Error(err, "unable to set up pprof handler")
			os.Exit(1)
		}
	}
}
