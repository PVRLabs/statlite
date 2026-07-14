package xyz.pvrlabs.actuatordemo;

import java.time.Duration;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api")
public class DemoController {

    private static final long MIN_DELAY_MS = 0;
    private static final long MAX_DELAY_MS = 5000;
    private static final long DEFAULT_DELAY_MS = 500;

    private final JdbcTemplate jdbcTemplate;

    public DemoController(JdbcTemplate jdbcTemplate) {
        this.jdbcTemplate = jdbcTemplate;
    }

    @GetMapping("/hello")
    public MessageResponse hello() {
        return new MessageResponse("Hello from the Spring Actuator demo");
    }

    @GetMapping("/slow")
    public ResponseEntity<?> slow(@RequestParam(name = "ms", defaultValue = "500") long ms) {
        long delayMs = clampDelay(ms);
        try {
            Thread.sleep(Duration.ofMillis(delayMs));
            return ResponseEntity.ok(new SlowResponse(delayMs, "Slow request completed"));
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
                .body(new ErrorResponse("Request interrupted"));
        }
    }

    @GetMapping("/db")
    public DbResponse db() {
        Integer count = jdbcTemplate.queryForObject("SELECT COUNT(*) FROM demo_item", Integer.class);
        return new DbResponse(count == null ? 0 : count);
    }

    @GetMapping("/bad-request")
    public ResponseEntity<ErrorResponse> badRequest() {
        return ResponseEntity.badRequest().body(new ErrorResponse("Demo bad request"));
    }

    @GetMapping("/error")
    public ResponseEntity<ErrorResponse> error() {
        return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
            .body(new ErrorResponse("Demo server error"));
    }

    private long clampDelay(long ms) {
        if (ms < MIN_DELAY_MS) {
            return MIN_DELAY_MS;
        }
        if (ms > MAX_DELAY_MS) {
            return MAX_DELAY_MS;
        }
        return ms;
    }

    public record MessageResponse(String message) {
    }

    public record SlowResponse(long delayMs, String message) {
    }

    public record DbResponse(int itemCount) {
    }

    public record ErrorResponse(String error) {
    }
}
