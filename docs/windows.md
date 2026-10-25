# Try StatLite on Windows

The Windows MVP targets Windows 10 or later on amd64 (x64). It is intended for
developers who want to monitor a local or remote application from a Windows
workstation. Run StatLite as a foreground
console application and open its dashboard in your browser. Linux, WSL and
Docker are not required.

Windows support is experimental. The executable cross-builds successfully,
and a native Windows CI workflow covers startup, Spring collection, SQLite
persistence and shutdown/restart. Its first Windows run is pending; Windows 10
desktop validation will follow separately. A published Windows download is not
yet available through this experiment.

## Get the executable

If you have been provided an experimental Windows amd64 build, put
`statlite.exe` in a writable folder such as a folder under your user profile.
Open PowerShell in that folder.

To build from source instead, install the Go version required by `go.mod`,
open PowerShell in the repository root, and run:

```powershell
$env:CGO_ENABLED = '0'
go build -o .\statlite.exe .\cmd\statlite
```

A C compiler is not required. The application you want to monitor must already
be running and expose a supported metrics endpoint.

## Monitor a Spring Boot application

For a local Spring Boot application with Actuator metrics exposed on port
8080, run:

```powershell
.\statlite.exe inspect 'http://localhost:8080' --create-config .\statlite.yaml
.\statlite.exe --config .\statlite.yaml
```

Open <http://127.0.0.1:9090>. Leave the terminal running while using StatLite.
Generate some requests to your application and allow a few polling intervals
for history to appear.

For a remote application, replace `http://localhost:8080` with its reachable
base URL. See [configuration](configuration.md) for endpoint exposure,
authentication and additional targets. Inspection does not accept credentials;
configure authenticated targets manually as described there.

`--create-config` writes only a new file. If `statlite.yaml` already exists,
reuse it, edit it, or choose another filename. Keep the dashboard bound to
`127.0.0.1` for local use.

## Stop, restart and keep history

Press Ctrl+C in the terminal to request shutdown. Start StatLite again with
the same configuration to reuse its history. Native Windows shutdown and
history recovery still need validation for this experimental build.

The generated configuration uses a local SQLite database. Relative database
paths are resolved from the configuration file's directory. Reuse that config on
subsequent runs, or configure an absolute path using forward slashes:

```yaml
storage:
  sqlite_path: 'C:/Users/your-name/StatLite/statlite.sqlite'
```

Use a writable local folder for the database. Do not delete the database if
you want to preserve history. Paths passed to PowerShell commands that contain
spaces must be quoted, for example `--config '.\my config\statlite.yaml'`.

## Available metrics and limits

Application metrics come from the monitored endpoint. Spring Boot may supply
CPU, memory and disk metrics for the environment running the application,
depending on which Actuator metrics it exposes.

Local Windows host CPU, memory and disk collection is deferred. StatLite
self-monitoring omits these values and records diagnostic warnings. Missing
resources appear as `No data` or their panels are hidden; they are not zero
measurements. Remote application metrics do not describe your workstation.

The MVP covers Windows amd64 console use. Windows Service integration, an MSI
installer and automatic startup are outside this trial.

If startup fails, check the terminal output, configuration path, database folder
permissions and whether another process already uses port 9090. For collection
failures, check that the configured application URL is reachable from Windows
and that the required metrics endpoints are exposed.

## Automated Windows validation

The [Windows workflow](../.github/workflows/windows.yml) runs native Go tests,
dashboard unit tests and a [lifecycle smoke test](../test/windows-smoke/README.md)
on Windows Server 2022. It uses the existing Spring Boot demo to verify actual
Actuator collection, then checks graceful shutdown and history across restarts.
The CI shutdown check sends Ctrl+Break to the test process group, which Go
handles through the same interrupt signal as Ctrl+C.

CI covers native Windows behavior. Manual Windows 10 validation will check the
minimum desktop version, browser presentation and interactive terminal usage.
