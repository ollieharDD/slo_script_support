package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/DataDog/datadog-api-client-go/api/v1/datadog"
)

const (
	// OneDay duration in hours
	OneDay = 24 * time.Hour
	// SevenDays duration in hours
	SevenDays = 7 * OneDay
	// ThirtyDays duration in hours
	ThirtyDays = 30 * OneDay
	// NinetyDays duration in hours
	NinetyDays = 90 * OneDay
)

// options struct to define options
var options struct {
	filePath string
	tagQuery string
	limit    int64
	sleep    time.Duration
}

func scriptUsage() {
	fmt.Printf("Usage: %s [OPTIONS] argument ...\n", os.Args[0])
	fmt.Println("\n Please make sure following environment variables are set DD_API_KEY and DD_APP_KEY")
	flag.PrintDefaults()
}

func init() {
	flag.StringVar(&options.filePath, "path", "/tmp/slo_report.csv", "path for csv file")
	flag.StringVar(&options.tagQuery, "tagQuery", "", "tag query to filter results based on a single SLO tag e.g team:ninja")
	flag.Int64Var(&options.limit, "limit", 1000, "limit SLOs fetched in each get_all call")
	flag.DurationVar(&options.sleep, "sleep", 100*time.Millisecond, "sleep time between slo history calls for each slo")
}

func main() {
	flag.Usage = scriptUsage
	flag.Parse()
	log.Printf("Please make sure following environment variables are set DD_API_KEY and DD_APP_KEY \n")
	log.Printf("SLO report file will be saved at: %s \n", options.filePath)

	limit := options.limit
	slos, err := getAllSLOs(limit, options.tagQuery)
	if err != nil {
		log.Printf("Error when calling `ServiceLevelObjectivesApi.ListSLOs`: %v\n", err)
	}

	log.Printf("Getting SLO History for %d SLOs ...", len(slos))
	generateReport(slos)
	log.Printf("Done - History retrived for %d SLOs", len(slos))
}

// creates a csv file and for each slo, adds slo status / error budget consumed details
func generateReport(slos []datadog.ServiceLevelObjective) {
	cols := []string{
		"name",
		"slo_id",
		"timeframe",
		"from (utc)",
		"to (utc)",
		"from_ts",
		"to_ts",
		"target",
		"overall_status",
		"error_budget_consumed",
		"error (only if applicable)",
	}
	// create file
	file, err := os.Create(options.filePath)
	if err != nil {
		log.Fatalf("Unable to create file: %s, err: %s", options.filePath, err)
	}

	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write(cols); err != nil {
		log.Fatalf("Unable to write to file: %s, err: %s", options.filePath, err)
	}

	ctx := datadog.NewDefaultContext(context.Background())
	configuration := datadog.NewConfiguration()
	configuration.SetUnstableOperationEnabled("GetSLOHistory", true)
	apiClient := datadog.NewAPIClient(configuration)
	now := time.Now().UTC()
	totalSlos := len(slos)
	for counter, slo := range slos {
		for _, threshold := range slo.Thresholds {
			log.Printf("(%d of %d) Getting SLO history s: %s, tf: %s", counter+1, totalSlos, slo.GetId(), threshold.Timeframe)
			from, to, err := getSLOTimeSpanFromTimeframe(threshold.Timeframe, now)
			// track and write error
			if err != nil {
				log.Printf(
					"Unable to get time span from timeframe s: %s, tf: %s, err: %s",
					slo.GetId(), threshold.Timeframe, err,
				)
				err := writeErr(writer, slo, threshold, from, to, err)
				if err != nil {
					log.Fatalf("Unable to write to file: %s, err: %s", options.filePath, err)
				}
				continue
			}

			// get slo history
			history, err := getSLOHistory(ctx, apiClient, slo, threshold, from, to)
			if err != nil {
				log.Printf(
					"Unable to get slo history s: %s, tf: %s, err: %s",
					slo.GetId(), threshold.GetTimeframe(), err,
				)
				err := writeErr(writer, slo, threshold, from, to, err)
				if err != nil {
					log.Fatalf("Unable to write to file: %s, err: %s", options.filePath, err)
				}
				continue
			}

			// write history to file
			err = writeHistory(writer, slo, threshold, *history, from, to)
			if err != nil {
				log.Printf(
					"Unable to write slo history details s: %s, tf: %s, err: %s",
					slo.GetId(), threshold.Timeframe, err,
				)
				err := writeErr(writer, slo, threshold, from, to, err)
				if err != nil {
					log.Fatalf("Unable to write to file: %s, err: %s", options.filePath, err)
				}
				continue
			}
			writer.Flush()
			time.Sleep(options.sleep)
		}
		time.Sleep(options.sleep)
	}
}

// getSLOHistory returns slo history
func getSLOHistory(
	ctx context.Context,
	apiClient *datadog.APIClient,
	slo datadog.ServiceLevelObjective,
	threshold datadog.SLOThreshold,
	from, to time.Time,
) (*datadog.SLOHistoryResponse, error) {
	optionalParams := datadog.GetSLOHistoryOptionalParameters{
		Target: &threshold.Target,
	}
	resp, _, err := apiClient.ServiceLevelObjectivesApi.GetSLOHistory(
		ctx,
		slo.GetId(),
		from.UTC().Unix(),
		to.UTC().Unix(),
		optionalParams,
	)
	if err != nil {
		return nil, err
	}

	// check for top level errors
	respErrors := resp.Errors
	if respErrors != nil {
		for _, err := range *respErrors {
			errStr := err.GetError()
			if errStr != "" {
				return nil, errors.New(errStr)
			}
		}
	}

	// make sure data is not nil
	if resp.Data == nil {
		return nil, errors.New("no history data received")
	}

	overallResp := resp.Data.Overall
	// make sure overall data is not nil
	if overallResp == nil {
		return nil, errors.New("no overall history received")
	}

	// check overall response errors
	if overallResp.Errors != nil {
		for _, overallErr := range *overallResp.Errors {
			if overallErr.ErrorMessage != "" {
				return nil, errors.New(overallErr.ErrorMessage)
			}
		}
	}

	return &resp, nil
}

// writeHistory write slo history to csv file
func writeHistory(
	writer *csv.Writer,
	slo datadog.ServiceLevelObjective,
	threshold datadog.SLOThreshold,
	history datadog.SLOHistoryResponse,
	from, to time.Time,
) error {
	overall := *history.Data.Overall
	errorBudgetRemainingMap := overall.GetErrorBudgetRemaining()
	// use custom since from/to is passed
	errorBudgetRemaining, found := errorBudgetRemainingMap["custom"]
	if !found {
		log.Printf("Unable to get error budget remaining s: %s, tf: %s", slo.GetId(), threshold.GetTimeframe())
		return errors.New("unable to get errror budget remaining")
	}

	data := []string{
		slo.GetName(),
		slo.GetId(),
		string(threshold.GetTimeframe()),
		fmt.Sprintf("%s", from.UTC()),
		fmt.Sprintf("%s", to.UTC()),
		fmt.Sprintf("%d", from.UTC().Unix()),
		fmt.Sprintf("%d", to.UTC().Unix()),
		fmt.Sprintf("%f", threshold.GetTarget()),
		fmt.Sprintf("%f", overall.GetSliValue()),
		fmt.Sprintf("%f", 100.0-errorBudgetRemaining),
		"",
	}
	return writer.Write(data)
}

// writeErr write error to csv file
func writeErr(
	writer *csv.Writer,
	slo datadog.ServiceLevelObjective,
	threshold datadog.SLOThreshold,
	from, to time.Time,
	err error,
) error {
	data := []string{
		slo.GetName(),
		slo.GetId(),
		string(threshold.GetTimeframe()),
		fmt.Sprintf("%s", from.UTC()),
		fmt.Sprintf("%s", to.UTC()),
		fmt.Sprintf("%d", from.UTC().Unix()),
		fmt.Sprintf("%d", to.UTC().Unix()),
		fmt.Sprintf("%f", threshold.GetTarget()),
		"",
		"",
		err.Error(),
	}
	return writer.Write(data)
}

// getAllSLOs returns all slos
func getAllSLOs(limit int64, tagQuery string) ([]datadog.ServiceLevelObjective, error) {
	ctx := datadog.NewDefaultContext(context.Background())
	offset := int64(0)
	configuration := datadog.NewConfiguration()
	apiClient := datadog.NewAPIClient(configuration)
	optionalParams := datadog.ListSLOsOptionalParameters{
		Limit:     &limit,
		Offset:    &offset,
		TagsQuery: &tagQuery,
	}

	var allSLOs []datadog.ServiceLevelObjective
	if tagQuery != "" {
		log.Printf("Querying SLOs for tag %s", tagQuery)
	}

	resp, _, err := apiClient.ServiceLevelObjectivesApi.ListSLOs(ctx, optionalParams)
	if err != nil {
		return []datadog.ServiceLevelObjective{}, err
	}
	slos := *resp.Data
	allSLOs = append(allSLOs, slos...)

	loaded := int64(len(*resp.Data))
	total := *resp.Metadata.Page.TotalCount
	log.Printf("Loaded %d SLOs, total SLOs %d \n", loaded, total)
	// load all slos
	for loaded < total {
		offset += loaded
		optionalParams.Offset = &offset
		resp, _, err := apiClient.ServiceLevelObjectivesApi.ListSLOs(ctx, optionalParams)
		if err != nil {
			return []datadog.ServiceLevelObjective{}, err
		}
		slos = *resp.Data
		allSLOs = append(allSLOs, slos...)
		loaded += int64(len(*resp.Data))
		log.Printf("Loaded %d SLOs, total SLOs %d \n", loaded, total)
		time.Sleep(1 * time.Second)
	}

	return allSLOs, nil
}

// getSLOTimeSpanFromTimeframe returns from/to time based on the slo timeframe
func getSLOTimeSpanFromTimeframe(tf datadog.SLOTimeframe, now time.Time) (time.Time, time.Time, error) {
	switch tf {
	case datadog.SLOTimeframe(datadog.SLOTIMEFRAME_SEVEN_DAYS):
		return now.Add(-SevenDays), now, nil
	case datadog.SLOTimeframe(datadog.SLOTIMEFRAME_THIRTY_DAYS):
		return now.Add(-ThirtyDays), now, nil
	case datadog.SLOTimeframe(datadog.SLOTIMEFRAME_NINETY_DAYS):
		return now.Add(-NinetyDays), now, nil
	}

	return time.Time{}, time.Time{}, fmt.Errorf("unsupported SLO timeframe : %s", tf)
}
