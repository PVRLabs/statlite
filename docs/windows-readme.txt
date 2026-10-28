StatLite for Windows (preview)
=============================
Requires Windows 10 or later, x64 (amd64). No Go, Java, Docker or WSL is
needed to run StatLite. Applications you monitor run separately.

Quick start
-----------
Extract the entire zip into a writable local folder under your user profile.
Open PowerShell in that folder and run:

  .\statlite.exe --config .\statlite.yaml

Open http://127.0.0.1:9090 in your browser. Leave the terminal running.
The included statlite.yaml is a complete self-monitoring configuration.
Allow a few 30-second polling intervals for history to appear.
Press Ctrl+C to stop; run the same command to restart.

History and configuration
-------------------------
History is stored in statlite-self.sqlite next to statlite.yaml. Keep this
database to retain history across restarts and upgrades. Stop StatLite before
replacing the executable; keep your configuration and database when upgrading.
Relative database paths resolve from the configuration file's directory.
Use forward slashes in YAML for absolute paths, e.g. C:/StatLite/history.sqlite.
Quote PowerShell paths containing spaces, e.g. --config '.\my folder\statlite.yaml'.
The folder must be writable. If port 9090 is busy, change both server.listen
and the self-monitoring target URL in statlite.yaml to the same unused port.
Keep the dashboard bound to 127.0.0.1 for local use.

Monitor an application
----------------------
If StatLite is already running, press Ctrl+C before continuing.

For a running Spring Boot application with Actuator exposed on port 8080:

  .\statlite.exe inspect http://localhost:8080 --create-config .\spring.yaml
  .\statlite.exe --config .\spring.yaml

Use a reachable application URL for remote monitoring. The config writer
requires a new filename; it will not overwrite the included statlite.yaml.
See the online configuration guide for credentials and additional targets.

Windows limits
--------------
StatLite runs as a foreground console application. Windows Service integration,
automatic startup, MSI installation and Windows ARM64 are not included.
Self-monitoring reports StatLite process and application metrics, but local
Windows host CPU, physical memory and disk metrics are unavailable. These
values are omitted with diagnostic warnings; panels show No data or are hidden.
Application metrics describe the monitored application's environment.

If startup or collection fails, check the terminal output, folder permissions,
port availability and connectivity to your application's metrics endpoint.

Documentation: https://github.com/PVRLabs/statlite/blob/main/docs/windows.md
Configuration: https://github.com/PVRLabs/statlite/blob/main/docs/configuration.md
