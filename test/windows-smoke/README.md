# Windows lifecycle smoke test

The Windows workflow runs the CGO-free executable against the existing Spring
Boot 3.5.5 demo and a StatLite self-monitoring target. It verifies:

- readiness and dashboard HTML;
- successful Spring collection, application health and runtime memory;
- missing local Windows host resources remain absent;
- graceful console-signal shutdown and listener closure;
- SQLite integrity and exact preservation of polls and raw metric samples
  across a restart with polling disabled; and
- new polls append after a normal restart without changing earlier history.

The test uses Python's standard library. CI packages the existing Spring demo
with Java 21 and Maven. From the repository root, after building the executable
and demo JAR, run:

```powershell
python test/windows-smoke/run.py --binary dist/statlite.exe --spring-jar examples/spring-actuator-demo/target/spring-actuator-demo-0.0.1-SNAPSHOT.jar
```

Each run writes JSON evidence, process logs and its disposable database below
`dist/windows-smoke/`. The workflow uploads JSON/logs and the executable, with
14-day retention. Build and test failures remain failures; artifact upload does
not change the job result.

On Windows the harness ensures a console exists, starts StatLite in its own
console process group, and sends `CTRL_BREAK_EVENT` to that group. Go delivers
this as `os.Interrupt`, the same signal its Ctrl+C handler receives. It requires
exit code zero within 15 seconds and checks that the listener closes. Forced
termination is used only for cleanup after a failure or for the demo process;
it never counts as successful StatLite shutdown.

GitHub's `windows-2022` runner is Windows Server 2022. This provides native
Windows runtime coverage, not Windows 10 desktop certification or a browser
visual test. The intended minimum is Windows 10 amd64; manual Windows 10
validation remains separate. Remote-network, authentication and desktop
terminal Ctrl+C checks are not established by this loopback smoke test.

The same harness can run on Linux/macOS for development, using SIGINT instead
of a Windows console event and allowing the existing local host metrics.
