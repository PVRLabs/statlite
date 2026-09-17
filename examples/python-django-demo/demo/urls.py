from django.http import JsonResponse
from django.urls import path

from demo.statlite_metrics import statlite_metrics_view


def hello_world(request):
    return JsonResponse({"message": "Hello, World!"})


def failure(request):
    raise RuntimeError("example failure")


urlpatterns = [
    path("", hello_world),
    path("failure", failure),
    path("statlite/metrics", statlite_metrics_view),
]
