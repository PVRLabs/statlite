package xyz.pvrlabs.actuatordemo;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
class ActuatorDemoApplicationTests {

    @Autowired
    private MockMvc mockMvc;

    @Test
    void helloReturnsExpectedMessage() throws Exception {
        mockMvc.perform(get("/api/hello"))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.message").value("Hello from the Spring Actuator demo"));
    }

    @Test
    void slowReturnsOk() throws Exception {
        mockMvc.perform(get("/api/slow").param("ms", "1"))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.delayMs").value(1));
    }

    @Test
    void dbReturnsRowCount() throws Exception {
        mockMvc.perform(get("/api/db"))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.itemCount").value(3));
    }

    @Test
    void badRequestReturns400() throws Exception {
        mockMvc.perform(get("/api/bad-request"))
            .andExpect(status().isBadRequest())
            .andExpect(jsonPath("$.error").value("Demo bad request"));
    }

    @Test
    void errorReturns500() throws Exception {
        mockMvc.perform(get("/api/error"))
            .andExpect(status().isInternalServerError())
            .andExpect(jsonPath("$.error").value("Demo server error"));
    }

    @Test
    void unknownRouteReturns404() throws Exception {
        mockMvc.perform(get("/does-not-exist"))
            .andExpect(status().isNotFound());
    }

    @Test
    void healthEndpointIsAvailableAndIncludesDatabase() throws Exception {
        mockMvc.perform(get("/actuator/health"))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.status").value("UP"))
            .andExpect(jsonPath("$.components.db.status").value("UP"));
    }

    @Test
    void metricsIndexIsAvailable() throws Exception {
        mockMvc.perform(get("/actuator/metrics"))
            .andExpect(status().isOk())
            .andExpect(jsonPath("$.names").isArray());
    }
}
