# WuKongIM API Documentation

This directory contains the API documentation for WuKongIM.

## Files

- `openapi.json` - OpenAPI 3.0 specification for the WuKongIM REST API
- `README.md` - This documentation file

## Accessing the API Documentation

### Swagger UI Interface

When the WuKongIM server is running, you can access the interactive API documentation at:

```
http://localhost:5001/docs
```

This provides a user-friendly Swagger UI interface where you can:

- Browse all API endpoints
- View detailed request/response schemas
- Test API endpoints directly from the browser
- Download the OpenAPI specification

### Direct OpenAPI Specification

You can also access the raw OpenAPI specification at:

```
http://localhost:5001/docs/openapi.json
```

## API Overview

The WuKongIM API provides the following functionality:

### System & Health
- Health checks and system status
- Migration status monitoring

### User Management
- User authentication and token management
- Online status tracking
- System user management

### Connection Management
- Connection removal and management
- Connection monitoring and statistics

### Channel Management
- Channel creation, update, and deletion
- Subscriber management
- Blacklist and whitelist management

### Message Management
- Message sending (single and batch)
- Message searching and retrieval
- Stream message handling

### Conversation Management
- Conversation synchronization
- Unread message management

### Monitoring & Metrics
- Connection statistics (`/connz`)
- System variables and metrics (`/varz`)

### Administrative
- Manager authentication
- System configuration

## Using the API

### Authentication

Most API endpoints require proper authentication. Refer to the specific endpoint documentation in the Swagger UI for authentication requirements.

### Base URL

The default base URL for the API is:
```
http://localhost:5001
```

### Content Type

Most endpoints expect and return JSON data:
```
Content-Type: application/json
```

### Error Handling

The API uses standard HTTP status codes and returns error information in JSON format:

```json
{
  "error": "Error description"
}
```

## Development

### Updating the Documentation

The OpenAPI specification is automatically generated based on the API route definitions in the codebase. To update the documentation:

1. Modify the API endpoints in the `internal/api/` directory
2. Update the `docs/openapi.json` file accordingly
3. Restart the WuKongIM server to see the changes

### Local Development

For local development, ensure the `docs/openapi.json` file is present in the project root or docs directory. The documentation endpoint will automatically locate and serve the specification file.

## Support

For questions about the API or this documentation, please refer to:

- [WuKongIM GitHub Repository](https://github.com/WuKongIM/WuKongIM)
- [WuKongIM Documentation](https://githubim.com)

## Version

This documentation is for WuKongIM API version 2.0.0.
