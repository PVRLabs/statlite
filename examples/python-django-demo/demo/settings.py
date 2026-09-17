SECRET_KEY = "statlite-demo-not-for-production"
DEBUG = False
ALLOWED_HOSTS = ["127.0.0.1", "localhost", "testserver"]
ROOT_URLCONF = "demo.urls"

INSTALLED_APPS = []

MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "demo.statlite_metrics.StatLiteMetricsMiddleware",
    "django.middleware.common.CommonMiddleware",
]

TEMPLATES = []
USE_TZ = True
