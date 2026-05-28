/*
The main package of WACE.
*/
package wace

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"

	"github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"

	"github.com/tilsor/ModSecIntl_logging/logging"

	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var plugins *pluginmanager.PluginManager
var ctx = context.Background()
var meter metric.Meter

// transactionSync is a struct to syncronize the analysis of a given
// transaction. Each time callPlugins is executed, the counter is
// incremented. At the end of each callPlugins execution, a message is
// sent through the channel, to signal checkTransaction that it has
// finished analyzing the request. checkTransaction waits for Counter
// number of messages in the channel, before calling the decision
// plugin and sending the result to the client.
type transactionSync struct {
	Channel chan string
	Counter int64
}

var (
	// Sync map witg channels to receive a notification when all plugins finish
	// processing a transaction
	analysisMap sync.Map
)

// addTransactionAnalysis adds a transaction to the analysis map. If the
// transaction already exists, it increments the counter of the transaction
// by one.
func addTransactionAnalysis(transactionID string) {
	tSync := transactionSync{
		Channel: make(chan string),
		Counter: 1,
	}
	value, loaded := analysisMap.LoadOrStore(transactionID, &tSync)
	if loaded {
		atomic.AddInt64(&value.(*transactionSync).Counter, 1)
	}
}

// callPlugins calls the model plugins in the given list, with the given input.
// It waits for all the synchronous model plugins to finish, and sends the
// result to the client. The asynchronous model plugins are executed in parallel
func callPlugins(input waceapi.HTTPPayload, models []string, t configstore.ModelPluginType, transactionID string) error {
	logger := logging.Get()

	// channel to receive the status of the execution of the analysis
	// of all the model plugins executed
	modelPluginStatus := make(chan pluginmanager.ModelStatus)
	asyncModelPluginStatus := make(chan pluginmanager.ModelStatus)

	plugins.AddModelChannel(transactionID, t, asyncModelPluginStatus, "async")
	plugins.AddModelChannel(transactionID, t, modelPluginStatus, "sync")

	conf, err := configstore.Get()
	if err != nil {
		return err
	}

	syncCounter := 0
	asyncCounter := 0

	startTime := time.Now()

	sanitizedPayload := sanitizeCredentials(input)

	for _, id := range models {
		logger.TPrintf(logging.DEBUG, transactionID, "%s | calling from core", id)

		if _, ok := conf.ModelPlugins[id]; !ok {
			logger.TPrintf(logging.ERROR, transactionID, "core | model plugin %s not found", id)
		} else if conf.ModelPlugins[id].PluginType != t {
			logger.TPrintf(logging.ERROR, transactionID, "core | model plugin %s is not of type %s", id, t)
		} else {
			payload := input
			if conf.ShouldSanitize(id) {
				payload = sanitizedPayload
			}
			if conf.IsAsync(id) {
				asyncCounter++
				go plugins.AddToQueue(id, transactionID, payload)
			} else if conf.IsInTraining(id) {
				go plugins.ProcessTraining(id, transactionID, payload, t)
			} else {
				if conf.IsRemote(id) {
					go plugins.AddToQueue(id, transactionID, payload)
				} else {
					go plugins.Process(id, transactionID, payload, t, modelPluginStatus)
				}
				syncCounter++
			}
		}

	}

	go func() {
		logger.TPrintf(logging.DEBUG, transactionID, "core | waiting for %d async model plugins to finish", asyncCounter)
		wg := sync.WaitGroup{}
		wg.Add(asyncCounter)
		for i := 0; i < asyncCounter; i++ {
			// Await for the execution of the async model plugins
			logger.TPrintf(logging.DEBUG, transactionID, "core | Waiting for async model plugin %d...", i+1)
			status := <-asyncModelPluginStatus
			if status.Err == nil {
				logger.TPrintf(logging.DEBUG, transactionID, "%s async | success. Result: %.5f", status.ModelID, status.ProbAttack)
				histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
				if err != nil {
					logger.TPrintf(logging.WARN, transactionID, "core | failed to record duration metric: %v", err.Error())
				}
				histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
					attribute.String("model_id", status.ModelID),
					attribute.String("model_mode", "async"),
					attribute.Float64("attack_probability", status.ProbAttack)))
			} else {
				logger.TPrintf(logging.WARN, transactionID, "%s | %v", status.ModelID, status.Err)
			}
			wg.Done()
		}
		wg.Wait()
		plugins.RemoveAsyncModelChannel(transactionID, t)
	}()

	logger.TPrintf(logging.DEBUG, transactionID, "core | waiting for %d sync model plugins to finish", syncCounter)
	for i := 0; i < syncCounter; i++ {
		// Await for the execution of the model plugins
		logger.TPrintf(logging.DEBUG, transactionID, "core | Waiting for sync model plugin %d...", i+1)
		status := <-modelPluginStatus
		if status.Err == nil {
			logger.TPrintf(logging.DEBUG, transactionID, "%s sync | success. Result: %.5f", status.ModelID, status.ProbAttack)

			histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
			if err != nil {
				logger.TPrintf(logging.WARN, transactionID, "core | failed to record duration metric: %v", err.Error())
			}
			histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
				attribute.String("model_id", status.ModelID),
				attribute.String("model_mode", "sync"),
				attribute.Float64("attack_probability", status.ProbAttack)))
		} else {
			logger.TPrintf(logging.WARN, transactionID, "%s | %v", status.ModelID, status.Err)
		}
	}

	value, ok := analysisMap.Load(transactionID)
	if !ok {
		logger.TPrintf(logging.ERROR, transactionID, "core | could not find transaction %s in analysis map", transactionID)
		return fmt.Errorf("core | could not find transaction %s in analysis map", transactionID)
	}
	analysisChan := value.(*transactionSync).Channel
	analysisChan <- "done"
	return nil
}

// InitTransaction initializes a transaction with the given id
func InitTransaction(transactionId string) {
	logger := logging.Get()
	logger.StartTransaction(transactionId)
	logger.TPrintf(logging.DEBUG, transactionId, "core | initializing transaction")
	tSync := transactionSync{
		Channel: make(chan string),
		Counter: 0,
	}
	analysisMap.Store(transactionId, &tSync)
	plugins.InitTransaction(transactionId)
}

// Analyze calls the model plugins with the given payload and models
func Analyze(modelsTypeAsString, transactionId string, payload waceapi.HTTPPayload, models []string) error {
	if len(models) > 0 {
		logger := logging.Get()
		modelsType, err := configstore.StringToPluginType(modelsTypeAsString)
		if err != nil {
			logger.TPrintf(logging.ERROR, transactionId, "core | %s is not a valid type", modelsTypeAsString)
			return err
		}
		logger.TPrintf(logging.DEBUG, transactionId, "core | analyzing %s: [%v...]", modelsTypeAsString, payload)
		addTransactionAnalysis(transactionId)
		go callPlugins(payload, models, modelsType, transactionId)
	}
	return nil
}

// CheckTransaction checks the result of the analysis of the transaction
// with the given id and decision plugin
func CheckTransaction(transactionID, decisionPlugin string, wafParams map[string]string) (bool, error) {
	logger := logging.Get()
	logger.TPrintf(logging.DEBUG, transactionID, "core | checking transaction")

	value, exists := analysisMap.Load(transactionID)

	if !exists {
		return false, fmt.Errorf("transaction with id %s does not exist", transactionID)
	}

	sync := value.(*transactionSync)

	logger.TPrintln(logging.DEBUG, transactionID, "core | waiting for all models to finish...")

	for i := 0; i < int(sync.Counter); i++ {
		<-sync.Channel
	}
	sync.Counter = 0

	logger.TPrintln(logging.DEBUG, transactionID, "core | done, checking data...")
	res, err := plugins.CheckResult(transactionID, decisionPlugin, wafParams)

	if err == nil {
		logger.TPrintf(logging.DEBUG, transactionID, "core | transaction checked successfully. Blocking transaction: %t", res)

		if res {
			metric, err := meter.Int64Counter("wace.client.request.blocked.total", metric.WithDescription(decisionPlugin))
			if err != nil {
				logger.TPrintf(logging.WARN, transactionID, "core | failed to record blocked request metric: %v", err.Error())
			}
			metric.Add(ctx, 1)
		}
	} else {
		logger.TPrintf(logging.ERROR, transactionID, "core | could not check transaction: %v", err)
	}
	return res, err
}

// CloseTransaction closes the transaction with the given id
// removing the transaction sync model results
func CloseTransaction(transactionID string) {
	plugins.CloseTransaction(transactionID)
	value, ok := analysisMap.Load(transactionID)
	logger := logging.Get()

	if !ok {
		logger.TPrintf(logging.ERROR, transactionID, "Analysis for transaction %s not found", transactionID)
	} else {
		close(value.(*transactionSync).Channel)
		for range value.(*transactionSync).Channel {
		}
		analysisMap.Delete(transactionID)
	}
}

// Reload applies a new configuration and reloads all plugins.
func Reload(met metric.Meter, conf configstore.ConfigFileData) error {
	logger := logging.Get()

	cs, err := configstore.Get()
	if err != nil {
		return err
	}
	if err = cs.SetConfig(conf); err != nil {
		return err
	}
	if len(cs.CredentialHeaders) != 0 {
		setCredentialHeaders(cs.CredentialHeaders)
	}
	if err = logger.LoadLogger(cs.LogPath, cs.LogLevel); err != nil {
		return err
	}
	meter = met
	return plugins.Reload(met)
}

// Init initializes the WACE core with the given metric meter
func Init(met metric.Meter, conf configstore.ConfigFileData) error {
	logger := logging.Get()

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

	err = logger.LoadLogger(cs.LogPath, cs.LogLevel)
	if err != nil {
		logger.Printf(logging.ERROR, "ERROR: could not open wace log file: %v", err)
		return err
	}
	logger.Printf(logging.DEBUG, "Writing logs to %s from now", cs.LogPath)

	logger.Println(logging.DEBUG, "Loading plugin manager...")
	plugins, err = pluginmanager.New(met)
	if err != nil {
		return err
	}
	logger.Println(logging.DEBUG, "Plugin manager loaded")

	return nil
}
