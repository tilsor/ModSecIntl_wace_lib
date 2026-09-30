/*
The main package of WACE.
*/
package wace

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"

	"github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"

	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var plugins *pluginmanager.PluginManager
var ctx = context.Background()
var meter metric.Meter

// coreLogger is the logger of the core, with component=core. It is
// replaced by Init and Reload.
var coreLogger atomic.Pointer[slog.Logger]

func init() {
	setLogger(slog.Default())
}

// setLogger stores l, with the core component attribute, as the core
// logger. l must not carry a component attribute.
func setLogger(l *slog.Logger) {
	coreLogger.Store(l.With(waceapi.LogKeyComponent, "core"))
}

// transactionSync is a struct to syncronize the analysis of a given
// transaction. Each time callPlugins is launched (once per Analyze
// call), wg.Add(1) is called. At the end of each callPlugins
// execution, wg.Done() is called, to signal CheckTransaction/
// CloseTransaction that it has finished analyzing the request.
// CheckTransaction and CloseTransaction call wg.Wait() to block until
// every in-flight callPlugins invocation for the transaction has
// finished, which guarantees CloseTransaction never tears down the
// plugin manager's channels while a model plugin goroutine may still
// be sending on them.
type transactionSync struct {
	wg sync.WaitGroup
}

var (
	// Sync map witg channels to receive a notification when all plugins finish
	// processing a transaction
	analysisMap sync.Map
)

// addTransactionAnalysis registers one more pending callPlugins invocation
// for the transaction, creating its transactionSync entry if needed.
func addTransactionAnalysis(transactionID string) {
	value, _ := analysisMap.LoadOrStore(transactionID, &transactionSync{})
	value.(*transactionSync).wg.Add(1)
}

// callPlugins calls the model plugins in the given list, with the given input.
// It waits for all the synchronous model plugins to finish, and sends the
// result to the client. The asynchronous model plugins are executed in parallel
func callPlugins(input waceapi.HTTPPayload, models []string, t configstore.ModelPluginType, transactionID string, logger *slog.Logger) error {
	value, ok := analysisMap.Load(transactionID)
	if !ok {
		logger.Error("could not find transaction in analysis map")
		return fmt.Errorf("core | could not find transaction %s in analysis map", transactionID)
	}
	defer value.(*transactionSync).wg.Done()

	conf, err := configstore.Get()
	if err != nil {
		return err
	}

	syncCounter := 0
	asyncCounter := 0
	filteredModels := make([]string, 0, len(models))
	for _, id := range models {
		mp, ok := conf.ModelPlugins[id]
		switch {
		case !ok:
			logger.Error("plugin not found",
				waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
				waceapi.LogKeyPlugin, id)
		case mp.PluginType != t:
			logger.Error("wrong plugin type",
				waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
				waceapi.LogKeyPlugin, id,
				"expected.type", t,
			)
		case conf.IsAsync(id):
			asyncCounter++
			filteredModels = append(filteredModels, id)
		case conf.IsInTraining(id):
			filteredModels = append(filteredModels, id)
		default:
			syncCounter++
			filteredModels = append(filteredModels, id)
		}
	}

	var modelPluginStatus chan pluginmanager.ModelStatus
	var asyncModelPluginStatus chan pluginmanager.ModelStatus
	if asyncCounter > 0 {
		asyncModelPluginStatus = make(chan pluginmanager.ModelStatus, asyncCounter)
		plugins.AddModelChannel(transactionID, t, asyncModelPluginStatus, "async")
	}
	if syncCounter > 0 {
		modelPluginStatus = make(chan pluginmanager.ModelStatus, syncCounter)
		plugins.AddModelChannel(transactionID, t, modelPluginStatus, "sync")
	}

	startTime := time.Now()

	sanitizedPayload := sanitizeCredentials(input)

	// A context without deadline never expires, so a zero timeout
	// waits for every sync model plugin.
	var ctx context.Context
	var cancel context.CancelFunc
	if conf.ModelTimeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), conf.ModelTimeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	for _, id := range filteredModels {
		logger.Debug("calling model plugin",
			waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
			waceapi.LogKeyPlugin, id)
		payload := input
		if conf.ShouldSanitize(id) {
			payload = sanitizedPayload
		}
		switch {
		case conf.IsAsync(id):
			go plugins.AddToQueue(id, transactionID, payload)
		case conf.IsInTraining(id):
			go plugins.ProcessTraining(id, transactionID, payload, t)
		case conf.IsRemote(id):
			go plugins.AddToQueue(id, transactionID, payload)
		default:
			go plugins.Process(id, transactionID, payload, t, modelPluginStatus)
		}
	}

	// Without async model plugins there is nothing to wait for.
	if asyncCounter > 0 {
		go func() {
			// The async plugins get their own context: the sync one is
			// cancelled as soon as callPlugins returns.
			var asyncCtx context.Context
			var asyncCancel context.CancelFunc
			if conf.AsyncModelTimeout > 0 {
				asyncCtx, asyncCancel = context.WithTimeout(context.Background(), conf.AsyncModelTimeout)
			} else {
				asyncCtx, asyncCancel = context.WithCancel(context.Background())
			}
			defer asyncCancel()

			logger.Debug("waiting for async model plugins to finish", "count", asyncCounter)
		waitAsync:
			for i := 0; i < asyncCounter; i++ {
				// Await for the execution of the async model plugins
				logger.Debug("waiting for async model plugin", "index", i+1)
				select {
				case status := <-asyncModelPluginStatus:
					if status.Err == nil {
						logger.Debug("model plugin succeeded",
							waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
							waceapi.LogKeyPlugin, status.ModelID,
							"plugin.mode", "async",
							"attack.probability", status.ProbAttack)
						histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
						if err != nil {
							logger.Warn("failed to record duration metric", "error", err)
						}
						histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
							attribute.String("model_id", status.ModelID),
							attribute.String("model_mode", "async"),
							attribute.Float64("attack_probability", status.ProbAttack)))
					} else {
						logger.Warn("model plugin failed",
							waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
							waceapi.LogKeyPlugin, status.ModelID,
							"plugin.mode", "async",
							"error", status.Err)
					}
				case <-asyncCtx.Done():
					logger.Warn("plugin call aborted due to timeout",
						"plugin.mode", "async",
						"pending", asyncCounter-i,
						"timeout", conf.AsyncModelTimeout)
					break waitAsync
				}
			}
			plugins.RemoveAsyncModelChannel(transactionID, t)
		}()
	}

	logger.Debug("waiting for sync model plugins to finish", "count", syncCounter)
waitSync:
	for i := 0; i < syncCounter; i++ {
		// Await for the execution of the model plugins
		logger.Debug("waiting for sync model plugin", "index", i+1)
		select {
		case status := <-modelPluginStatus:
			if status.Err == nil {
				logger.Debug("model plugin succeeded",
					waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
					waceapi.LogKeyPlugin, status.ModelID,
					"plugin.mode", "sync",
					"attack.probability", status.ProbAttack)

				histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
				if err != nil {
					logger.Warn("failed to record duration metric", "error", err)
				}
				histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
					attribute.String("model_id", status.ModelID),
					attribute.String("model_mode", "sync"),
					attribute.Float64("attack_probability", status.ProbAttack)))
			} else {
				logger.Warn("model plugin failed",
					waceapi.LogKeyPluginType, waceapi.LogValueModelPluginType,
					waceapi.LogKeyPlugin, status.ModelID,
					"plugin.mode", "sync",
					"error", status.Err)
			}
		case <-ctx.Done():
			logger.Warn("plugin call aborted due to timeout",
				"plugin.mode", "sync",
				"pending", syncCounter-i,
				"timeout", conf.ModelTimeout)
			break waitSync
		}
	}

	return nil
}

// InitTransaction initializes a transaction with the given id
func InitTransaction(transactionId string) {
	coreLogger.Load().Debug("initializing transaction", waceapi.LogKeyTxID, transactionId)
	analysisMap.Store(transactionId, &transactionSync{})
	plugins.InitTransaction(transactionId)
}

// Analyze calls the model plugins with the given payload and models
func Analyze(modelsTypeAsString, transactionId string, payload waceapi.HTTPPayload, models []string) error {
	if len(models) > 0 {
		logger := coreLogger.Load().With(waceapi.LogKeyTxID, transactionId)
		modelsType, err := configstore.StringToPluginType(modelsTypeAsString)
		if err != nil {
			logger.Error("invalid model plugin type", "type", modelsTypeAsString)
			return err
		}
		logger.Debug("analyzing payload", "type", modelsTypeAsString, "payload", payload)
		addTransactionAnalysis(transactionId)
		go callPlugins(payload, models, modelsType, transactionId, logger)
	}
	return nil
}

// CheckTransaction checks the result of the analysis of the transaction
// with the given id and decision plugin
func CheckTransaction(transactionID string, decisionPlugins []string, wafData waceapi.WAFData) (bool, bool, error) {
	logger := coreLogger.Load().With(waceapi.LogKeyTxID, transactionID)
	logger.Debug("checking transaction")

	value, exists := analysisMap.Load(transactionID)

	if !exists {
		return false, false, fmt.Errorf("transaction with id %s does not exist", transactionID)
	}

	tSync := value.(*transactionSync)

	logger.Debug("waiting for all models to finish")

	tSync.wg.Wait()

	logger.Debug("models finished, checking results")
	res, enabledPluginFound, err := plugins.CheckResult(transactionID, decisionPlugins, wafData)

	if err == nil {
		logger.Debug("transaction checked successfully", "block", res)

		if res {
			metric, err := meter.Int64Counter("wace.client.request.blocked.total", metric.WithDescription(fmt.Sprintf("%v", decisionPlugins)))
			if err != nil {
				logger.Warn("failed to record blocked request metric", "error", err)
			}
			metric.Add(ctx, 1)
		}
	} else {
		logger.Error("could not check transaction", "error", err)
	}
	return res, enabledPluginFound, err
}

// CloseTransaction closes the transaction with the given id
// removing the transaction sync model results. It waits for any
// callPlugins invocation still in flight for this transaction to
// finish before tearing down the plugin manager's channels, so a
// model plugin goroutine never sends on a channel that has already
// been closed.
func CloseTransaction(transactionID string) {
	value, ok := analysisMap.Load(transactionID)

	if !ok {
		coreLogger.Load().Error("analysis for transaction not found", waceapi.LogKeyTxID, transactionID)
		return
	}

	value.(*transactionSync).wg.Wait()
	plugins.CloseTransaction(transactionID)
	analysisMap.Delete(transactionID)
}

// Reload applies a new configuration and logger, and reloads all
// plugins. If l is nil, slog.Default() is used. If the configuration is
// rejected, the logger is not replaced either.
func Reload(met metric.Meter, conf configstore.ConfigFileData, l *slog.Logger) error {
	if l == nil {
		l = slog.Default()
	}

	cs, err := configstore.Get()
	if err != nil {
		return err
	}
	if err = cs.SetConfig(conf); err != nil {
		return err
	}
	setLogger(l)
	if len(cs.CredentialHeaders) != 0 {
		setCredentialHeaders(cs.CredentialHeaders)
	}
	meter = met
	return plugins.Reload(met, l)
}

// Init initializes the WACE core with the given metric meter and
// logger. If l is nil, slog.Default() is used.
func Init(met metric.Meter, conf configstore.ConfigFileData, l *slog.Logger) error {
	if l == nil {
		l = slog.Default()
	}
	setLogger(l)
	logger := coreLogger.Load()

	cs, err := configstore.New()
	if err != nil {
		return err
	}

	err = cs.SetConfig(conf)
	if err != nil {
		return err
	}

	if len(cs.CredentialHeaders) != 0 {
		setCredentialHeaders(cs.CredentialHeaders)
	}

	meter = met

	logger.Debug("loading plugin manager")
	// pass l, not the core logger: pluginmanager adds its own component attribute
	plugins, err = pluginmanager.New(met, l)
	if err != nil {
		return err
	}
	logger.Debug("plugin manager loaded")

	return nil
}
