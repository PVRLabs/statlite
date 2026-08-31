package xyz.pvrlabs.quarkusmetricsdemo;

import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.core.Response;

@Path("/")
public class DemoResource {
    @GET
    @Path("ok")
    public String ok() { return "ok"; }

    @GET
    @Path("missing")
    public Response missing() { return Response.status(404).build(); }

    @GET
    @Path("client-error")
    public Response clientError() { return Response.status(418).build(); }

    @GET
    @Path("server-error")
    public Response serverError() { return Response.serverError().build(); }
}
