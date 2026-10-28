# StatLite on Windows

The Windows preview targets Windows 10 or later on amd64 (x64). It is intended for
developers who want to monitor a local or remote application from a Windows
workstation. Run StatLite as a foreground
console application and open its dashboard in your browser. Linux, WSL and
Docker are not required.

## Get the executable

Download `statlite_<version>_windows_amd64.zip` and its `.sha256` file from
[GitHub Releases](https://github.com/PVRLabs/statlite/releases/latest).
In PowerShell, compare the archive's hash with the value in the checksum file:

```powershell
Get-FileHash .\statlite_<version>_windows_amd64.zip -Algorithm SHA256
Get-Content .\statlite_<version>_windows_amd64.zip.sha256
```

Replace `<version>` with the downloaded version. Extract the entire zip into a
writable local folder under your user profile and open PowerShell there.
The zip contains `statlite.exe`, `readme.txt`, `statlite.yaml` and third-party
license notices. Start with the included self-monitoring configuration:

```powershell
.\statlite.exe --config .\statlite.yaml
```

Open <http://127.0.0.1:9090>. Allow a few 30-second polling intervals for history.
Local Windows host resources are unavailable, as described below.

To build from source instead, install the Go version required by `go.mod`,
open PowerShell in the repository root, and run:

```powershell
$env:CGO_ENABLED = '0'
go build -o .\statlite.exe .\cmd\statlite
```

A C compiler is not required. The application you want to monitor must already
be running and expose a supported metrics endpoint.

## Monitor a Spring Boot application

If StatLite is already running, press Ctrl+C before continuing.

For a local Spring Boot application with Actuator metrics exposed on port
8080, run:

```powershell
.\statlite.exe inspect 'http://localhost:8080' --create-config .\spring.yaml
.\statlite.exe --config .\spring.yaml
```

Open <http://127.0.0.1:9090>. Leave the terminal running while using StatLite.
Generate some requests to your application and allow a few polling intervals
for history to appear.

For a remote application, replace `http://localhost:8080` with its reachable
base URL. See [configuration](configuration.md) for endpoint exposure,
authentication and additional targets. Inspection does not accept credentials;
configure authenticated targets manually as described there.

`--create-config` writes only a new file. If `spring.yaml` already exists,
reuse it, edit it, or choose another filename. Keep the dashboard bound to
`127.0.0.1` for local use.

## Stop, restart and keep history

Press Ctrl+C in the terminal to request shutdown. Start StatLite again with
the same configuration to reuse its history. Keep your configuration and database
when upgrading; stop StatLite before replacing the executable.

The bundled configuration stores history in `statlite-self.sqlite` next to
`statlite.yaml`. Generated application configurations also use a local SQLite
database. Relative database paths are resolved from the configuration file's directory. Reuse that config on
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

The preview covers Windows amd64 console use. Windows Service integration, an MSI
installer and automatic startup are not included.

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
