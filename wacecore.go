/*
The main package of WACE.
*/
package wace

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cf "github.com/tiroa-tilsor/wacelib/configstore"

	pm "github.com/tiroa-tilsor/wacelib/pluginmanager"

	lg "github.com/tilsor/ModSecIntl_logging/logging"

	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var plugins *pm.PluginManager
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
func callPlugins(input string, models []string, t cf.ModelPluginType, transactionId string) {
	logger := lg.Get()

	// channel to receive the status of the execution of the analysis
	// of all the model plugins executed
	modelPlugStatus := make(chan pm.ModelStatus)
	asyncModelPlugStatus := make(chan pm.ModelStatus)

	plugins.AddModelChannel(transactionId, t, asyncModelPlugStatus, "async")
	plugins.AddModelChannel(transactionId, t, modelPlugStatus, "sync")

	conf := cf.Get()

	syncCounter := 0
	asyncCounter := 0

	startTime := time.Now()

	for _, id := range models {
		logger.TPrintf(lg.DEBUG, transactionId, "%s | calling from core", id)
		if _, ok := conf.ModelPlugins[id]; !ok {
			logger.TPrintf(lg.ERROR, transactionId, "core | model plugin %s not found", id)
		} else {
			if conf.ModelPlugins[id].PluginType != t {
				logger.TPrintf(lg.ERROR, transactionId, "core | model plugin %s is not of type %s", id, t)
			} else {
				if conf.IsAsync(id) {
					asyncCounter++
					go plugins.AddToQueue(id, transactionId, input)
				} else {
					if conf.ModelPlugins[id].Remote {
						go plugins.AddToQueue(id, transactionId, input)
					} else {
						go plugins.Process(id, transactionId, input, t, modelPlugStatus)
					}
					syncCounter++
				}
			}
		}
	}

	go func() {
		logger.TPrintf(lg.DEBUG, transactionId, "core | waiting for %d async model plugins to finish", asyncCounter)
		wg := sync.WaitGroup{}
		wg.Add(asyncCounter)
		for i := 0; i < asyncCounter; i++ {
			// Await for the execution of the async model plugins
			logger.TPrintf(lg.DEBUG, transactionId, "core | Waiting for async model plugin %d...", i+1)
			status := <-asyncModelPlugStatus
			if status.Err == nil {
				logger.TPrintf(lg.DEBUG, transactionId, "%s async | success. Result: %.5f", status.ModelID, status.ProbAttack)
				histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
				if err != nil {
					logger.TPrintf(lg.WARN, transactionId, "core | failed to record duration metric: %v", err.Error())
				}
				histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
					attribute.String("model_id", status.ModelID),
					attribute.String("model_mode", "async"),
					attribute.Float64("attack_probability", status.ProbAttack)))
			} else {
				logger.TPrintf(lg.WARN, transactionId, "%s | %v", status.ModelID, status.Err)
			}
			wg.Done()
		}
		wg.Wait()
		plugins.RemoveAsyncModelChannel(transactionId, t)
	}()

	logger.TPrintf(lg.DEBUG, transactionId, "core | waiting for %d sync model plugins to finish", syncCounter)
	for i := 0; i < syncCounter; i++ {
		// Await for the execution of the model plugins
		logger.TPrintf(lg.DEBUG, transactionId, "core | Waiting for sync model plugin %d...", i+1)
		status := <-modelPlugStatus
		if status.Err == nil {
			logger.TPrintf(lg.DEBUG, transactionId, "%s sync | success. Result: %.5f", status.ModelID, status.ProbAttack)

			histogramMeter, err := meter.Int64Histogram("wace.model.duration.nanoseconds")
			if err != nil {
				logger.TPrintf(lg.WARN, transactionId, "core | failed to record duration metric: %v", err.Error())
			}
			histogramMeter.Record(ctx, time.Since(startTime).Nanoseconds(), metric.WithAttributes(
				attribute.String("model_id", status.ModelID),
				attribute.String("model_mode", "sync"),
				attribute.Float64("attack_probability", status.ProbAttack)))
		} else {
			logger.TPrintf(lg.WARN, transactionId, "%s | %v", status.ModelID, status.Err)
		}
	}

	value, ok := analysisMap.Load(transactionId)
	if !ok {
		logger.TPrintf(lg.ERROR, transactionId, "core | could not find transaction %s in analysis map", transactionId)
		return
	}
	analysisChan := value.(*transactionSync).Channel
	analysisChan <- "done"
}

// InitTransaction initializes a transaction with the given id
func InitTransaction(transactionId string) {
	logger := lg.Get()
	logger.StartTransaction(transactionId)
	logger.TPrintf(lg.DEBUG, transactionId, "core | initializing transaction")
	tSync := transactionSync{
		Channel: make(chan string),
		Counter: 0,
	}
	analysisMap.Store(transactionId, &tSync)
	plugins.InitTransaction(transactionId)
}

// Analyze calls the model plugins with the given payload and models
func Analyze(modelsTypeAsString, transactionId, payload string, models []string) error {
	if len(models) > 0 {
		logger := lg.Get()
		modelsType, err := cf.StringToPluginType(modelsTypeAsString)
		if err != nil {
			logger.TPrintf(lg.ERROR, transactionId, "core | %s is not a valid type", modelsTypeAsString)
			return err
		}
		logger.TPrintf(lg.DEBUG, transactionId, "core | analyzing %s: [%s...]", modelsTypeAsString, strings.Split(payload, "\n")[0])
		addTransactionAnalysis(transactionId)
		go callPlugins(payload, models, modelsType, transactionId)
	}
	return nil
}

// CheckTransaction checks the result of the analysis of the transaction
// with the given id and decision plugin
func CheckTransaction(transactionID, decisionPlugin string, wafParams map[string]string) (bool, error) {
	logger := lg.Get()
	logger.TPrintf(lg.DEBUG, transactionID, "core | checking transaction")

	value, exists := analysisMap.Load(transactionID)

	if !exists {
		return false, fmt.Errorf("transaction with id %s does not exist", transactionID)
	}

	sync := value.(*transactionSync)

	logger.TPrintln(lg.DEBUG, transactionID, "core | waiting for all models to finish...")

	for i := 0; i < int(sync.Counter); i++ {
		<-sync.Channel
	}
	sync.Counter = 0

	logger.TPrintln(lg.DEBUG, transactionID, "core | done, checking data...")
	res, err := plugins.CheckResult(transactionID, decisionPlugin, wafParams)

	if err == nil {
		logger.TPrintf(lg.DEBUG, transactionID, "core | transaction checked successfully. Blocking transaction: %t", res)

		if res {
			metric, err := meter.Int64Counter("wace.client.request.blocked.total", metric.WithDescription(decisionPlugin))
			if err != nil {
				logger.TPrintf(lg.WARN, transactionID, "core | failed to record blocked request metric: %v", err.Error())
			}
			metric.Add(ctx, 1)
		}
	} else {
		logger.TPrintf(lg.ERROR, transactionID, "core | could not check transaction: %v", err)
	}
	return res, err
}

// CloseTransaction closes the transaction with the given id
// removing the transaction sync model results
func CloseTransaction(transactionID string) {
	plugins.CloseTransaction(transactionID)
	value, ok := analysisMap.Load(transactionID)
	logger := lg.Get()
	
	if !ok {
		logger.TPrintf(lg.ERROR, transactionID, "Analysis for transaction %s not found", transactionID)
	} else {
		close(value.(*transactionSync).Channel)
		for range value.(*transactionSync).Channel {}
		analysisMap.Delete(transactionID)
	}
}

// Init initializes the WACE core with the given metric meter
func Init(met metric.Meter) {
	logger := lg.Get()
	conf := cf.Get()
	meter = met

	err := logger.LoadLogger(conf.LogPath, conf.LogLevel)
	if err != nil {
		logger.Printf(lg.ERROR, "ERROR: could not open wace log file: %v", err)
		os.Exit(1)

	}
	logger.Printf(lg.DEBUG, "Writing logs to %s from now", conf.LogPath)

	logger.Println(lg.DEBUG, "Loading plugin manager...")
	plugins = pm.New(met)
	logger.Println(lg.DEBUG, "Plugin manager loaded")
}
