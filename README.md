# Discipline Backend

A Go-based backend service for managing discipline-related operations with authentication and user management capabilities.

## Table of Contents
- [Project Overview](#project-overview)
- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [API Documentation](#api-documentation)
- [Project Structure](#project-structure)
- [Environment Variables](#environment-variables)
- [Contributing](#contributing)
- [License](#license)

## Project Overview

This is a Go web application built with Gin framework that provides a backend service for discipline management. The application includes user authentication, role-based access control, and follows clean architecture principles with separation of concerns.

## Features

- User authentication with JWT tokens
- Role-based access control (RBAC)
- RESTful API endpoints
- Database integration
- Middleware for authentication and authorization
- Swagger documentation
- Clean architecture with separated layers (handlers, services, repositories, models)

## Prerequisites

- Go 1.19 or higher
- Git
- A database system (MySQL, PostgreSQL, or SQLite)

## Installation

1. Clone the repository:
   ```bash
   git clone <your-repo-url>
   cd discipline-back
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

3. Set up your environment variables (see [Environment Variables](#environment-variables))

4. Run the application:
   ```bash
   go run cmd/server/main.go
   ```

## Configuration

The application uses a configuration system located in the `config/` directory. Make sure to set up your database connection and other settings in the configuration files.

## Usage

Once the server is running, you can access the API endpoints. By default, the server runs on `http://localhost:8080`.

### API Endpoints

- `/api/v1/auth/login` - User login
- `/api/v1/auth/register` - User registration
- Other endpoints as configured in `internal/routes/routes.go`

Swagger documentation is available at `/swagger/index.html` when the server runs with `ENABLE_DOCS=true`.

## API Documentation

The API is documented using Swagger. Start the server with `ENABLE_DOCS=true`, then visit `http://localhost:8080/swagger/index.html` to view the interactive API documentation.

## Project Structure

```
discipline-back/
├── cmd/
│   └── server/
│       └── main.go                 # Application entry point
├── config/
│   ├── config.go                   # Main configuration
│   ├── database.go                 # Database configuration
│   └── seed.go                     # Data seeding utilities
├── docs/
│   ├── docs.go                     # Swagger documentation
│   ├── swagger.json                # Swagger JSON
│   └── swagger.yaml                # Swagger YAML
├── internal/
│   ├── handlers/                   # HTTP request handlers
│   │   └── auth_handler.go         # Authentication handlers
│   ├── middleware/                 # HTTP middleware
│   │   ├── auth_middleware.go      # Authentication middleware
│   │   └── role_middleware.go      # Role-based access middleware
│   ├── models/                     # Data models
│   │   └── user.go                 # User model
│   ├── repositories/               # Database repositories
│   │   └── user_repository.go      # User repository
│   ├── routes/                     # Route definitions
│   │   └── routes.go               # API route configuration
│   └── services/                   # Business logic
│       └── auth_service.go         # Authentication services
├── pkg/                            # Shared packages
├── go.mod                          # Go module definition
├── go.sum                          # Go module checksums
└── .env                            # Environment variables (not committed)
```

## Environment Variables

Create a `.env` file in the root directory (it is git-ignored).

See `.env.example` for every variable (names only). Required, with no defaults —
the server refuses to start without them:

- `JWT_SECRET` (at least 32 characters)
- `INTERNAL_JWT_SECRET` (at least 32 characters, different from `JWT_SECRET`, same value on the analytics service)
- `DB_PASSWORD`
- `PYTHON_SERVICE_URL`

For local development also set `APP_ENV=development` and, if your local Postgres has no TLS, `DB_SSLMODE=disable`.

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Commit your changes (`git commit -m 'Add some amazing feature'`)
5. Push to the branch (`git push origin feature/amazing-feature`)
6. Open a Pull Request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.