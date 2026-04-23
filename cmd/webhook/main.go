package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/maheshvaidya/k8s-admission-controller/internal/controller"
	"github.com/maheshvaidya/k8s-admission-controller/internal/handler"
)

func main() {
	// Logger setup
	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&opts))
	ctrllog.SetLogger(log)

	log.Info("starting pdb-webhook")

	// Read environment variables
	tlsCertFile := getEnv("TLS_CERT_FILE", "/tls/tls.crt")
	tlsKeyFile := getEnv("TLS_KEY_FILE", "/tls/tls.key")
	listenPort := getEnv("LISTEN_PORT", ":8443")

	// Setup scheme
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "failed to add clientgo scheme")
		os.Exit(1)
	}
	if err := admissionv1.AddToScheme(scheme); err != nil {
		log.Error(err, "failed to add admission scheme")
		os.Exit(1)
	}
	if err := policyv1.AddToScheme(scheme); err != nil {
		log.Error(err, "failed to add policy scheme")
		os.Exit(1)
	}

	// Create Kubernetes client
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Error(err, "failed to get in-cluster config")
		os.Exit(1)
	}

	kubeClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "failed to create kubernetes client")
		os.Exit(1)
	}

	// Setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Setup controller manager for namespace reconciliation
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server
		},
	})
	if err != nil {
		log.Error(err, "failed to create controller manager")
		os.Exit(1)
	}

	// Setup namespace controller
	if err := (&controller.NamespaceReconciler{
		Client: mgr.GetClient(),
		Log:    log.WithName("namespace-controller"),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "failed to setup namespace controller")
		os.Exit(1)
	}

	// Start controller manager in a goroutine
	go func() {
		if err := mgr.Start(ctx); err != nil {
			log.Error(err, "controller manager exited with error")
		}
	}()

	// Setup HTTP routes (use direct client, not cached manager client)
	validateHandler := handler.NewHandler(kubeClient, log)
	mutateHandler := handler.NewMutatingHandler(kubeClient, log)
	http.HandleFunc("/validate", validateHandler.Handle)
	http.HandleFunc("/mutate", mutateHandler.Handle)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Create TLS server
	server := &http.Server{
		Addr:      listenPort,
		Handler:   http.DefaultServeMux,
		TLSConfig: nil, // Will use tlsCertFile and tlsKeyFile
	}

	// Graceful shutdown handling
	go func() {
		<-ctx.Done()
		log.Info("received shutdown signal, gracefully shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error(err, "error during server shutdown")
		}
	}()

	log.Info("starting TLS server", "addr", listenPort)
	if err := server.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil && err != http.ErrServerClosed {
		log.Error(err, "server error")
		os.Exit(1)
	}

	log.Info("server stopped")
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}
