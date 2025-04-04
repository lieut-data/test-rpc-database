# RPC Database Test Plugin

This plugin is designed to test and compare database performance between RPC-based and direct database access in Mattermost plugins.

It creates a test table in the Mattermost database, inserts 50,000 records, and measures the time it takes to query this table with configurable batch sizes.

## Features

- Creates a `plugin_test_rpc` table in the Mattermost database
- Inserts 50,000 test records if they don't exist
- Provides two API endpoints for performance comparison:
  - `/api/v1/test`: Uses Mattermost's RPC-based database access via StoreService
  - `/api/v1/test_raw`: Uses direct SQL connection to the database
- Supports configurable page sizes via the `page_size` query parameter
- Returns detailed timing information in JSON format

## Usage

After installing the plugin, you can access the database test endpoints at:

```
# Test using RPC-based database access
<your-mattermost-url>/plugins/com.mattermost.test-rpc-database/api/v1/test

# Test using direct database connection
<your-mattermost-url>/plugins/com.mattermost.test-rpc-database/api/v1/test_raw
```

### Query Parameters

- `page_size`: Number of records to fetch in each database query (default: 100)
  - Example: `/api/v1/test?page_size=10000`

### API Response Example

```json
{
  "insert_time_seconds": 0,
  "total_query_time_seconds": 0.587083,
  "conn_type": "rpc",
  "records_queried": 50000,
  "page_size": 100
}
```

## Performance Comparison

The plugin allows comparing performance between two database access methods:

1. **RPC-based Connection** (via Mattermost's StoreService):  
   Accesses the database through Mattermost's plugin API abstractions.
   
2. **Direct SQL Connection**:  
   Establishes a direct connection to the database using the database credentials from Mattermost's config.

Performance varies significantly based on the page size:

| Page Size | RPC-based Time (sec) | Direct SQL Time (sec) | Performance Difference |
|-----------|----------------------|-----------------------|------------------------|
| 100       | ~3.26                | ~0.68                 | ~4.8x faster          |
| 10,000    | ~1.55                | ~0.04                 | ~38x faster           |
| 50,000    | ~1.51                | ~0.03                 | ~50x faster           |

## Development

### Building the Plugin

```
make
```

This will produce a single plugin file for upload to your Mattermost server:

```
dist/com.mattermost.test-rpc-database.tar.gz
```

### Deploying with Local Mode

If your Mattermost server is running locally with local mode enabled:

```
make deploy
```

### Using an Experimental Version of Mattermost

To test with a local/experimental version of Mattermost, edit the `go.mod` file to include this replace directive:

```
replace github.com/mattermost/mattermost/server/public => /path/to/local/mattermost/server/public
```

Then run `go mod tidy` followed by `make deploy` to build and deploy with the modified dependencies.