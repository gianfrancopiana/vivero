#!/bin/sh
set -eu
state="${FAKE_DOCKER_STATE:?}"
mkdir -p "$state"
cmd="${1:-}"
if [ $# -gt 0 ]; then shift; fi
case "$cmd" in
  container)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    name="${1:-}"
    case "$sub" in
      inspect)
        if [ -f "$state/$name.pid" ]; then echo "$name"; exit 0; fi
        echo "No such container: $name" >&2
        exit 1
        ;;
    esac
    echo "unsupported docker container command $sub" >&2
    exit 2
    ;;
  network)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    name="${1:-}"
    case "$sub" in
      inspect)
        if [ -f "$state/network-$name" ]; then exit 0; fi
        echo "No such network: $name" >&2
        exit 1
        ;;
      create)
        touch "$state/network-$name"
        echo "$name"
        exit 0
        ;;
      rm)
        if [ "${FAKE_DOCKER_NETWORK_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_NETWORK_RM_FAIL" >&2; exit 7; fi
        rm -f "$state/network-$name"
        exit 0
        ;;
    esac
    echo "unsupported docker network command $sub" >&2
    exit 2
    ;;
  volume)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    name="${1:-}"
    case "$sub" in
      inspect)
        if [ -f "$state/volume-$name" ]; then echo "$name"; exit 0; fi
        echo "No such volume: $name" >&2
        exit 1
        ;;
      create)
        touch "$state/volume-$name"
        echo "$name"
        exit 0
        ;;
      rm)
        if [ "${FAKE_DOCKER_VOLUME_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_VOLUME_RM_FAIL" >&2; exit 7; fi
        rm -f "$state/volume-$name"
        echo "$name"
        exit 0
        ;;
    esac
    echo "unsupported docker volume command $sub" >&2
    exit 2
    ;;
  build)
    tag=""
    dockerfile=""
    context=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --tag|-t) tag="$2"; shift 2 ;;
        --file|-f) dockerfile="$2"; shift 2 ;;
        --build-arg) shift 2 ;;
        --) shift; break ;;
        -*) echo "unsupported docker build flag $1" >&2; exit 2 ;;
        *) context="$1"; shift ;;
      esac
    done
    printf '%s|%s|%s\n' "${tag:-untagged}" "$dockerfile" "$context" >> "$state/builds"
    echo "built ${tag:-untagged}"
    exit 0
    ;;
  buildx)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    case "$sub" in
      version)
        if [ "${FAKE_DOCKER_BUILDX_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_BUILDX_FAIL" >&2; exit 7; fi
        echo "github.com/docker/buildx v0.12.0"
        exit 0
        ;;
      build)
        tag=""
        dockerfile=""
        context=""
        cache_from=""
        cache_to=""
        while [ $# -gt 0 ]; do
          case "$1" in
            --load) shift ;;
            --tag|-t) tag="$2"; shift 2 ;;
            --file|-f) dockerfile="$2"; shift 2 ;;
            --build-arg) shift 2 ;;
            --cache-from) cache_from="${cache_from}${cache_from:+,}$2"; shift 2 ;;
            --cache-to) cache_to="${cache_to}${cache_to:+,}$2"; shift 2 ;;
            --) shift; break ;;
            -*) echo "unsupported docker buildx build flag $1" >&2; exit 2 ;;
            *) context="$1"; shift ;;
          esac
        done
        printf 'buildx|%s|%s|%s|%s|%s\n' "${tag:-untagged}" "$dockerfile" "$context" "$cache_from" "$cache_to" >> "$state/builds"
        echo "built ${tag:-untagged}"
        exit 0
        ;;
    esac
    echo "unsupported docker buildx command $sub" >&2
    exit 2
    ;;
  ps)
    preview_filter=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -a|-q|-aq|-qa) shift ;;
        --filter)
          case "$2" in
            label=vivero.preview=*) preview_filter="${2#label=vivero.preview=}" ;;
          esac
          shift 2
          ;;
        *) shift ;;
      esac
    done
    for f in "$state"/*.pid; do
      [ -e "$f" ] || continue
      name="$(basename "$f" .pid)"
      if [ -n "$preview_filter" ]; then
        [ -f "$state/$name.preview" ] || continue
        [ "$(cat "$state/$name.preview")" = "$preview_filter" ] || continue
      fi
      echo "$name"
    done
    exit 0
    ;;
  rm)
    if [ "${FAKE_DOCKER_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_RM_FAIL" >&2; exit 7; fi
    if [ "${1:-}" = "-f" ]; then shift; fi
    while [ $# -gt 0 ]; do
      name="${1:-}"
      if [ -n "$name" ] && [ -f "$state/$name.pid" ]; then
        kill "$(cat "$state/$name.pid")" 2>/dev/null || true
        rm -f "$state/$name.pid" "$state/$name.preview" "$state/$name.service" "$state/$name.cwd"
      fi
      shift || true
    done
    exit 0
    ;;
  logs)
    if [ "${1:-}" = "--tail" ]; then shift 2; fi
    name="${1:-}"
    if [ -f "$state/$name.log" ]; then cat "$state/$name.log"; fi
    exit 0
    ;;
  port)
    name="${1:-}"
    query="${2:-}"
    if [ -f "$state/$name.ports" ]; then
      awk -F'|' -v q="$query" '$1 == q { print $2; found=1; exit } END { exit found ? 0 : 1 }' "$state/$name.ports"
      exit $?
    fi
    exit 1
    ;;
  exec)
    name="${1:-}"
    shift || true
    cwd="$(cat "$state/$name.cwd" 2>/dev/null || pwd)"
    cd "$cwd"
    exec "$@"
    ;;
  run)
    if [ "${FAKE_DOCKER_REJECT_DOCKER_HOST:-}" != "" ] && [ "${DOCKER_HOST:-}" = "$FAKE_DOCKER_REJECT_DOCKER_HOST" ]; then
      echo "docker client env leaked DOCKER_HOST" >&2
      exit 9
    fi
    detach=0
    name=""
    volume=""
    workdir=""
    preview_label=""
    service_label=""
    published=""
    mount_count=0
    while [ $# -gt 0 ]; do
      case "$1" in
        --rm) shift ;;
        --detach|-d) detach=1; shift ;;
        --name) name="$2"; shift 2 ;;
        --volume|-v) volume="$2"; shift 2 ;;
        --workdir|-w) workdir="$2"; shift 2 ;;
        --label)
          case "$2" in
            vivero.preview=*) preview_label="${2#vivero.preview=}" ;;
            vivero.service=*) service_label="${2#vivero.service=}" ;;
          esac
          shift 2
          ;;
        --publish) published="$published
$2"; shift 2 ;;
        --mount) mount_count=$((mount_count + 1)); shift 2 ;;
        --cpus|--memory|--env|--env-file|--network|--network-alias) shift 2 ;;
        --) shift; break ;;
        -*) echo "unsupported docker flag $1" >&2; exit 2 ;;
        *) image="$1"; shift; break ;;
      esac
    done
    : "${image:=fake-image}"
    if [ -z "$name" ]; then name="fake-container"; fi
    hostwork="$workdir"
    case "$volume" in
      *:/app)
        hostroot="${volume%:/app}"
        case "$workdir" in
          /app*) hostwork="$hostroot${workdir#/app}" ;;
          "") hostwork="$hostroot" ;;
        esac
        ;;
    esac
    if [ -z "$hostwork" ]; then hostwork="$(pwd)"; fi
    mkdir -p "$hostwork"
    printf '%s' "$hostwork" > "$state/$name.cwd"
    [ -n "$preview_label" ] && printf '%s' "$preview_label" > "$state/$name.preview"
    [ -n "$service_label" ] && printf '%s' "$service_label" > "$state/$name.service"
    : > "$state/$name.ports"
    printf '%s\n' "$published" | while IFS= read -r publish; do
      [ -n "$publish" ] || continue
      container="${publish##*:}"
      hostpart="${publish%:*}"
      hostport="${hostpart##*:}"
      hostip="${hostpart%:*}"
      if [ "$hostip" = "$hostpart" ]; then hostip="127.0.0.1"; fi
      if [ -z "$hostport" ]; then hostport="${container%%/*}"; fi
      protocol="tcp"
      case "$container" in */*) protocol="${container#*/}"; container="${container%%/*}" ;; esac
      printf '%s/%s|%s:%s\n' "$container" "$protocol" "$hostip" "$hostport" >> "$state/$name.ports"
    done
    if [ "$detach" = "1" ]; then
      (
        cd "$hostwork"
        "$@" > "$state/$name.log" 2>&1 &
        echo $! > "$state/$name.pid"
      )
      if [ "${FAKE_DOCKER_WARN:-}" != "" ]; then echo "$FAKE_DOCKER_WARN" >&2; fi
      echo "$name"
      exit 0
    fi
    if [ "$name" = "fake-container" ] && [ "$mount_count" -gt 0 ]; then
      echo "copied volumes"
      exit 0
    fi
    cd "$hostwork"
    exec "$@"
    ;;
esac
echo "unsupported docker command $cmd" >&2
exit 2
