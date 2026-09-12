# Varnish in front of the Mecone origin. Nothing but the backend is declared, so every caching
# decision is made by Varnish's built-in VCL.

vcl 4.1;

backend default {
    .host = "${ORIGIN_HOST}";
    .port = "${ORIGIN_PORT}";
}
