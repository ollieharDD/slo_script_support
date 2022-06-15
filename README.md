## ENV details 
This script has been tested with Go version 1.5

## Requirments
1. Please make sure following environment variables are set DD_API_KEY (your api key) and DD_APP_KEY (your app key)

## Build the binary 
1. cd into directory and run `go build main.go` to generate binary file named main
2. Run `./main --help` to see usage

```
./main --help
Usage: ./main [OPTIONS] argument ...

 Please make sure following environment variables are set DD_API_KEY and DD_APP_KEY
  -limit int
    	limit SLOs fetched in each get_all call (default 1000)
  -path string
    	path for csv file (default "/tmp/slo_report.csv")
  -sleep duration
    	sleep time between slo history calls for each slo (default 100ms)
  -tagQuery string
    	tag query e.g env:prod
```
## To run this script

3. Run `./main /path/to/report.csv`

