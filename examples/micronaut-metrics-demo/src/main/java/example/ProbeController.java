package example;

import io.micronaut.http.HttpResponse;
import io.micronaut.http.annotation.Controller;
import io.micronaut.http.annotation.Get;

@Controller("/probe")
public class ProbeController {
    @Get("/ok")
    public String ok() { return "ok"; }

    @Get("/bad")
    public HttpResponse<String> bad() { return HttpResponse.badRequest("bad"); }

    @Get("/error")
    public HttpResponse<String> error() { return HttpResponse.serverError("error"); }
}
