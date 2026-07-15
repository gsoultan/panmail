# Panmail Project Guidelines

This document provides project-specific information and development standards for working on Panmail. It is optimized for both developers and AI agents.

## 🚀 Environment & Workflow

### Environment Setup
- **Go Version**: Developed and tested with **Go 1.26.3**.
- **Protobuf Tooling**: **Buf** (required for gRPC code generation).
- **Frontend Tooling**: Latest **Bun** (required for builds), **Vite 8**, and **TypeScript**.
- **Pathing**: If `go` is not available in the default `PATH` on macOS, it is typically located at `/opt/homebrew/bin/go`.

### Common Commands (Always use `rtk` prefix)
| Action | Command |
| :--- | :--- |
| **Initialization** | `rtk go mod tidy` |
| **Build (All)** | `rtk make build` |
| **Build (Backend)** | `rtk go build -tags builtui -o panmail ./cmd/api` |
| **Test** | `rtk go test -v ./...` |
| **gRPC Generate** | `rtk buf generate` |
| **Full Generate** | `rtk make generate` |

### 🤖 AI Agent Workflow
To ensure maximum efficiency and adherence to project standards, AI agents MUST:
1.  **Load Guidelines**: Always read `.junie/agents.md` and `.junie/rtk.md` at the start of every task.
2.  **Token Optimization**: Always use the **Rust Toolkit Killer (RTK)** by prefixing ALL terminal commands with `rtk`.
    - Example: `rtk git status`, `rtk go test ./...`, `rtk ls -R`.
    - RTK significantly reduces token usage (60-90% savings) and filters output for better readability.
3.  **Production-Grade Output**: Always generate code that is production-grade. Avoid placeholders, "TODO" comments, or partial implementations. Every line of code must contribute to the mission of building the best email gateway.

## 👤 Developer Profiles

To achieve the mission of building the **best email gateway**—characterized by **best performance**, **lightweight** footprint, and **great UI/UX**—Junie operates as a multi-disciplinary team of senior experts working in synergy:

- **Tech Lead**: Oversees the technical direction, ensuring all components work together seamlessly to create a world-class email gateway.
- **Senior Software Architect**: Designs scalable and maintainable systems using modern patterns and strong OOP principles.
- **Senior Golang Backend Engineer**: Expert in building robust, high-performance, and thread-safe Go services.
- **Senior React JS Engineer**: Specialized in creating modular, performant frontend applications using React 19 and Mantine v9.
- **Senior Optimization Engineer**: Focuses on performance tuning, minimizing latency, and ensuring a lightweight footprint with zero allocations.
- **Senior Test & Debugger Engineer**: Ensures extreme reliability through comprehensive table-driven tests and meticulous debugging.
- **Senior UI/UX Engineer**: Crafts intuitive, systematic, and beautiful interfaces with a focus on clarity and "Fool-Proof" design.
- **Senior Infrastructure Engineer**: Manages the build ecosystem, gRPC health protocols, and ensures optimal system environment.
- **Senior Product Manager**: Bridges the gap between technical excellence and user needs, prioritizing features that define the best email gateway.

---

## 📂 Project Structure

Adhere to the standard Go project layout to ensure scalability and maintainability:

- **`/cmd`**: Main entry points for the application. Each subdirectory should represent a separate executable (e.g., `cmd/api/main.go`).
- **`/internal`**: Private application code that should not be imported by other projects. This is where the core logic lives.
- **`/pkg`**: Public reusable code that can be shared with other projects. Use sparingly.
- **`/api`**: API definitions, schemas (OpenAPI/Swagger), and protocol buffer files.
- **`/scripts`**: Scripts for build, installation, and other automation tasks.
- **`/configs`**: Configuration files and templates.

**Rule**: Favor domain-based organization within `internal/` (e.g., `internal/user`, `internal/mail`) rather than grouping by technical layer (e.g., `internal/handlers`, `internal/models`).
**Rule**: Use descriptive package names and avoid generic names like `util` or `common`.

### 🏗️ Layered Architecture Pattern
Adhere to the following layered structure within domain-based packages to ensure a clear separation of concerns and a consistent execution flow:

- **Transports**: Entry points for external communication.
    - *Types*: `grpc`, `http`, `message_queue`, `sse`, `websocket`.
- **Middlewares**: Components for cross-cutting concerns.
    - *Types*: `authentication`, `logger`, `instrumentation`.
- **Endpoints**: The interface between transport and business logic.
- **Services**: Orchestration layer based on service definitions and domains.
    - **Rule**: One service can manage multiple **Usecases**.
    - **Service Facade**: Use a facade pattern to group multiple services, providing a simplified interface to the domain logic.
- **Usecases**: Atomic business logic operations.
- **Repositories**: Data access layer for interacting with databases or external data sources.
    - **Structure**: The `repositories` folder must consist of exactly two sub-folders:
        1.  **`entities`**: Contains database-specific models or entities.
        2.  **`stores`**: Contains repository implementations, separated per database vendor (e.g., `stores/postgres`, `stores/mysql`).
    - **SQL Sub-folders**: Each database-specific folder within `stores/` must contain a `sql/` sub-folder for storing `.sql` files.
- **Multi-DB Support**: The system must support PostgreSQL, MySQL, MariaDB, and SQLite.
- **Pebble Logging**: Use Pebble (KV store) for high-performance log storage.

**Pattern Flow**: `Transports` → `Middlewares` → `Endpoints` → `Services` → `Usecases` → `Repositories`.

### File & Folder Readability Limits
To maintain a clean and navigable workspace, follow these thresholds for directory organization:

- **The "No-Scroll" Rule**: Keep the number of files in a single folder to a maximum of **10 files**.
- **Miller's Law (7±2)**: Keep the number of top-level folders and immediate child directories manageable (ideally 5 to 9) to reduce cognitive load.
- **Threshold for Refactoring**:
    - **< 10 files**: Ideal for most focused packages.
    - **10 files**: Maximum allowed capacity.
    - **> 10 files**: Mandatory refactoring; split the folder into sub-packages or sub-directories based on domain or functionality.
- **Single Responsibility**: Every folder should represent a single cohesive concept or domain. If a folder contains unrelated files, split it regardless of the file count.

---

## ⚛️ React & Frontend Standards

### 🛠️ Tech Stack
- **Framework**: Latest **React 19**.
- **Build Tool**: Latest **Vite 8** using **Bun** for builds.
- **Language**: Latest **TypeScript**.
- **UI Library**: Latest **Mantine v9**.
- **Routing**: **TanStack Router**.
- **Data Fetching**: **TanStack Query** and **ConnectRPC**.
- **State Management**: Latest **Zustand** for global state management.

### 🚀 Performance & Standards
- **Lazy Loading**: Mandatory for all page-level components and large modules.
- **Bundle Size**: Compiled files must not exceed **500kb**.

### 🎨 UI/UX Design Principles
- **User-Centric (Fool-Proof)**: Design intuitive interfaces that minimize cognitive load and prevent user errors.
- **Systematic & Beautiful**: Adhere to a systematic design language to create professional, aesthetically pleasing interfaces.
- **Clarity**: Ensure the UI is easy to understand at a glance, with a clear hierarchy and prioritized information.
- **Mantine Optimization**: Maximize the use of Mantine UI components, ensuring they are optimized for performance and speed.
- **Human-Readable Error Handling**: Provide clear, actionable error messages instead of technical jargon.
- **Clear Notifications**: Ensure all user notifications are concise, timely, and easily understood.
- **Application Shell**: Use Mantine's `AppShell` for the main layout.
    - **Header**: Contains the application logo/title on the left and a User Profile menu on the right.
    - **Navbar**: A left-hand menu for primary navigation.
    - **Profile Menu**: Includes "My Profile" and "Sign Out" options.
- **First-Run Setup**: Implement a guided setup wizard for initial configuration (Database, Admin User).

### 📂 Folder Structure
For React-based frontends, follow a feature-driven and modular folder structure to ensure maintainability and scalability.

- **`/src`**: Main source code directory.
    - **`/assets`**: Static assets like images, fonts, and global styles.
    - **`/components`**: Shared, reusable UI components (e.g., `Button`, `Modal`, `Table`).
    - **`/features`**: The core of the application, organized by domain/feature (e.g., `auth`, `users`, `mail`).
    - **`/hooks`**: Global custom React hooks.
    - **`/layouts`**: Reusable page layouts (e.g., `DashboardLayout`, `AuthLayout`).
    - **`/pages`**: Route-level components that compose features and layouts.
    - **`/services`**: Global API clients, data fetching logic, and external service integrations.
    - **`/store`**: Global state management (e.g., Redux, Zustand, Context providers).
    - **`/utils`**: Pure utility functions and helpers.
    - **`/types`**: Global TypeScript type definitions and interfaces.

### 🧩 Modular Component Pattern
Every non-trivial component should have its own folder to keep related files together:

```text
Component/
├── index.ts          # Public API for the component
├── Component.tsx      # Component logic and JSX
├── Component.test.tsx # Unit tests
├── styles.css         # Component-specific styles (e.g., CSS Modules)
└── types.ts           # Component-specific types
```

**Rule**: Favor **Feature-Based** organization. Move logic, components, and hooks into `features/` if they are specific to a single domain.
**Rule**: Keep the `components/` folder for truly generic, reusable UI elements.

---

## 🧪 Testing Standards

### 📂 Test Organization
- **Unit Tests**: Place `_test.go` files in the same package as the code they test. Use the `package name` for white-box testing or `package name_test` for black-box testing.
- **Integration Tests**: Place cross-package or end-to-end tests in a dedicated `tests/` directory at the project root (e.g., `tests/integration`).
- **Test Data**: Store external fixtures, golden files, or mock data in a `testdata` folder within the package. Go's toolchain ignores this folder.

### 🛠️ Best Practices
- **Table-Driven Tests**: Use table-driven tests with anonymous structs for multiple test cases to reduce boilerplate.
- **Subtests**: Always use `t.Run(tc.name, ...)` for each case in a table-driven test for clear failure reporting.
- **Race Detection**: Always run tests with the race detector enabled: `rtk go test -race ./...`.
- **Modern Context**: Use `t.Context()` when a test requires a context to handle timeouts and cancellations correctly (Go 1.24+).
- **Avoid Global State**: Tests should be isolated; avoid relying on or modifying global state.

### 📝 Verified Test Pattern (Table-Driven)
```go
package domain

import "testing"

func TestCalculate(t *testing.T) {
    tests := []struct {
        name     string
        input    int
        expected int
    }{
        {"Positive", 1, 2},
        {"Zero", 0, 1},
        {"Negative", -1, 0},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            got := Calculate(tc.input)
            if got != tc.expected {
                t.Errorf("%s: Calculate(%d) = %d; want %d", tc.name, tc.input, got, tc.expected)
            }
        })
    }
}
```

---

## 📐 Development Standards

### 💎 Production-Grade Standards
- **Production-Ready**: AI agents MUST always generate code that is production-grade. This means:
    - **No Shortcuts**: Avoid "TODO"s, "FIXME"s, or "placeholders" unless explicitly requested for a prototype.
    - **Complete Logic**: Implement all edge cases, error handling, and validation logic.
    - **Observability**: Include structured logging and appropriate instrumentation.
    - **Maintainability**: Code must be modular, self-documenting, and follow the established patterns.
    - **Resilience**: Ensure proper use of timeouts, retries, and circuit breakers where external systems are involved.

### Code Formatting & Quality
- Strictly adhere to standard Go formatting and **Clean Code** principles.
- **Tools**:
    - Run `rtk gofmt -w .` to format the code.
    - Use `rtk goimports` to manage imports and format the code.
    - Always run `rtk go vet ./...` to catch common mistakes before committing.
- Run formatting and quality checks before any commit.

### IDE Integration
- The project contains GoLand-specific configuration in the `.idea` folder.
- **Recommendation**: Using GoLand is highly recommended for an optimal development workflow.

### 🏛️ Software Architecture & Patterns
- **Strong Object-Oriented Programming (OOP)**: Code must adhere to strong OOP principles adapted for Go:
    - **Encapsulation**: Use unexported fields in structs to hide internal state; provide exported constructor functions (e.g., `NewService`) and getter/setter methods where necessary.
    - **Behavior**: Define logic as methods on structs rather than standalone functions to group data and behavior.
    - **Polymorphism**: Leverage interfaces to define behavior and allow for multiple implementations.
- **Programming by Interface**: Always favor programming by interface to ensure decoupling and easier testability.
    - Define interfaces where consumers need to use abstractions.
    - Follow the Go idiom: **"Accept interfaces, return structs"**.
    - **Rule**: One interface per file; one struct per file.
- **Design Patterns**: Utilize standard design patterns (e.g., Factory, Singleton, Strategy, Decorator) where appropriate to solve recurring problems and improve code structure. Avoid over-engineering; use patterns that simplify the codebase.

### 📡 API & Communication
- **gRPC & Protobuf**: All service-to-service communication must use gRPC.
- **Frontend Communication**: All frontend-to-backend communication must use **ConnectRPC**.
- **Buf**: Use **Buf** exclusively for managing Protobuf files, linting, and generating Go code.
    - Protobuf definitions must be stored in the `/api` directory.
    - Ensure `buf.yaml` and `buf.gen.yaml` are correctly configured in the project root or `/api`.

#### 📜 Protobuf Best Practices
- **Style Guide**:
    - **Messages**: Use `TitleCase` for message names (e.g., `EmailRequest`).
    - **Fields**: Use `snake_case` for field names (e.g., `subject`).
    - **Repeated Fields**: Use pluralized names for repeated fields (e.g., `repeated string tags = 1;`).
    - **Indentation**: Use 2 spaces for indentation.
    - **Line Length**: Keep line length to 80 characters.
    - **Packages**: Use `lower_snake_case` for package names (e.g., `package panmail.v1;`).
- **Enums**:
    - Use `TitleCase` for enum names and `UPPER_SNAKE_CASE` for values.
    - **Zero Value**: The first enum value MUST be `0` and named `TYPE_UNSPECIFIED`.
    - **Prefixing**: Prefix enum values with the enum type name to avoid naming collisions.
- **Schema Evolution**:
    - **Never Reuse Tags**: Never reuse a tag number for different fields.
    - **Reserved Tags**: Use the `reserved` keyword for deleted field numbers and names to prevent future reuse.
    - **Compatibility**: Ensure changes are backward-compatible; clients and servers are rarely updated simultaneously.
- **Structure**:
    - **1-1-1 Rule**: Ideally, define only one top-level entity (message, enum, or service) per `.proto` file.
    - **Folder Hierarchy**: Organize files by domain and version (e.g., `api/user/v1/user.proto`).
    - **Coupled Messages**: Group messages in the same file ONLY if they are extremely conceptually coupled or to resolve circular dependencies.
    - Files should be named `lower_snake_case.proto`.
    - Keep messages focused; if a message has too many fields (e.g., >20), consider breaking it down.

#### 🏥 gRPC Health Checking
- **Standard Protocol**: All gRPC servers MUST implement the [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md).
- **Implementation**:
    - Use the official Go health package: `google.golang.org/grpc/health`.
    - Register the health service using `grpc_health_v1.RegisterHealthServer`.
- **Status Management**:
    - Use `health.NewServer()` to create a health server instance.
    - Set serving status for each service independently using `SetServingStatus`.
    - **Dependencies**: Health checks should reflect the state of critical dependencies (e.g., database, cache).
- **Graceful Shutdown**: Always mark the service as `NOT_SERVING` before the server stops to notify clients and load balancers.
- **Performance**: Health check responses should be fast (typically < 100ms) to avoid blocking probes.

### 🗃️ Constants & SQL
- **No Magic Strings**: Always use constants or variables for strings; never hardcode strings directly in the logic.
- **SQL Externalization**: All SQL queries must be stored in separate `.sql` files. Do not embed raw SQL strings directly in Go source code.
- **SQL Compilation**: The separated `.sql` files MUST be compiled into the Go binary using the standard `//go:embed` directive (from the `embed` package). Do not read `.sql` files from the filesystem at runtime; embed them at build time to ensure a single self-contained binary.
    - Example:
      ```go
      import _ "embed"

      //go:embed sql/get_user_by_id.sql
      var getUserByIDQuery string
      ```

### 💡 Key Readability Rules for Go Functions
To ensure code quality and maintainability, all functions must adhere to the following principles:

1.  **Single Responsibility Principle (SRP)**
    - A function must do exactly one thing.
    - If a function has sections separated by comments, extract them into independent helper functions.
2.  **Minimize Cognitive Complexity**
    - Keep nesting levels (if, for, switch) to a minimum.
    - Favor the **"happy path"**: return early upon encountering errors instead of deeply nesting successful execution.
3.  **Keep Parameters Low**
    - **Rule**: Maximum of **3 parameters** per function.
    - If a function requires more than 3 parameters, group them into a struct or decompose the function.
4.  **Short Declared Variables**
    - Variables should have short lifetimes.
    - Keep functions short to ensure variables are declared close to where they are used.
5.  **Limit Function Length**
    - A function should ideally be a maximum of 50 lines.
    - Composite the function if it is more than 50 lines.

### 💡 Key Readability Rules for Structs & Interfaces
To maintain clarity and high cohesion:
1.  **Interface Method Limit**: Maximum **15 functions** in one interface.
2.  **Struct Method Limit**: Maximum **15 functions** in one struct.

---

## 🔒 Security, Performance & Modern Syntax

### 🛡️ Security First
- **Mandatory**: Security is a non-negotiable, first-class requirement. Generating secure code takes priority over convenience; never trade security for speed or simplicity.
- **Input Validation**: Validate, sanitize, and constrain ALL external input (request bodies, query params, headers, env vars, file contents) at the boundary before use. Reject by default and allow-list known-good values.
- **Injection Prevention**: Always use parameterized/prepared statements for SQL; never build queries via string concatenation. Escape or sanitize any data used in shell commands, templates, or other interpreters.
- **Secrets Management**: Never hardcode secrets, credentials, tokens, or keys. Load them from environment variables or a secrets manager, and keep them out of logs, errors, and version control.
- **AuthN & AuthZ**: Authenticate every request and enforce least-privilege authorization on every endpoint. Deny by default and verify permissions server-side.
- **Cryptography**: Use vetted, standard-library or well-maintained crypto (e.g., `crypto/*`); never roll your own. Use strong algorithms, secure randomness (`crypto/rand`), and enforce TLS for data in transit.
- **Safe Error Handling**: Never leak sensitive data, stack traces, or internal details in error messages or API responses. Log securely and fail closed.
- **Dependencies**: Keep dependencies minimal and up to date; scan for known vulnerabilities (e.g., `rtk govulncheck`) and avoid untrusted or unmaintained packages.
- **Safe APIs**: Prefer memory-safe, well-audited APIs; avoid the `unsafe` package and dangerous patterns unless strictly justified and reviewed.

### 🧼 Clean Code
- **Clean Code**: Write self-documenting code with meaningful names. If a comment is needed to explain *what* the code does, the code should probably be refactored.

### 🔐 Type Safety & Thread Safety
- **Mandatory**: All Go code MUST be **type-safe** and **thread-safe**.
- **Type Safety**:
    - Leverage Go's static type system; prefer concrete types and generics over `any` to catch errors at compile time.
    - Avoid unsafe type assertions; always use the comma-ok form (e.g., `v, ok := x.(T)`) or `errors.AsType[T]` and handle the failure case.
    - Avoid the `unsafe` package unless strictly necessary and clearly justified.
- **Thread Safety**:
    - Protect shared mutable state with synchronization primitives (`sync.Mutex`, `sync.RWMutex`) or atomic types (`sync.atomic`).
    - Favor communicating via channels over sharing memory; ensure every goroutine has a clear lifecycle and is cancellable via `context.Context`.
    - All exported APIs that may be accessed concurrently MUST be safe for concurrent use, and their concurrency guarantees documented.
    - Always run tests with the race detector enabled (`rtk go test -race ./...`) to verify thread safety.

### 🚀 Performance & Optimization
- **Efficiency**: Optimize for both execution speed and memory usage.
- **Zero Allocations**: Favor stack allocation and object reuse (e.g., `sync.Pool`) in performance-critical paths.
- **Concurrency**: Use goroutines and channels judiciously. Always ensure goroutines have a clear lifecycle and can be cancelled.

### ⚡ Modern Go Syntax (1.26+)
- **Idiomatic Go**: Use modern Go syntax and idioms (e.g., `any`, generics, `errors.Is`, `slices.Contains`, `maps.Keys`).
- **Generics**: Use generics (type parameters) to write reusable and type-safe code; avoid redundant code or unnecessary use of `any` when generics can be applied.
- **New Features**: Leverage Go 1.26 specific features like `for range` over integers, `max`/`min` functions, and `omitzero` struct tags.
- **Context**: Always propagate `context.Context` correctly and use `t.Context()` in tests.
