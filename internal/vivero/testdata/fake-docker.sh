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
    case "$sub" in
      inspect)
		formatted=""
		if [ "${1:-}" = "--format" ]; then
		  formatted="${2:-}"
		  shift 2
		fi
		name="${1:-}"
		if [ -f "$state/$name.pid" ]; then
		  if [ -n "$formatted" ]; then
			if [ -f "$state/$name.exited" ]; then running="false"; else running="true"; fi
			case "$formatted" in
			  *vivero.compose.expected-completion*)
				exit_code="0"
				if [ -f "$state/$name.exited" ]; then exit_code="$(cat "$state/$name.exit-code" 2>/dev/null || echo 1)"; fi
				expected=""
				if [ -f "$state/$name.expected-completion" ]; then expected="true"; fi
				printf '%s|%s|%s\n' "$running" "$exit_code" "$expected"
				;;
			  *ExitCode*)
				exit_code="0"
				if [ -f "$state/$name.exited" ]; then exit_code="$(cat "$state/$name.exit-code" 2>/dev/null || echo 1)"; fi
				printf '%s %s\n' "$running" "$exit_code"
				;;
			  *) echo "$running" ;;
			esac
		  else
			echo "$name"
		  fi
		  exit 0
		fi
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
		rm -f "$state/$name.compose-project"
        exit 0
        ;;
	  ls)
		compose_filter=""
		while [ $# -gt 0 ]; do
		  case "$1" in
			-q) shift ;;
			--filter)
			  case "$2" in
				label=com.docker.compose.project=*) compose_filter="${2#label=com.docker.compose.project=}" ;;
			  esac
			  shift 2
			  ;;
			*) shift ;;
		  esac
		done
		for f in "$state"/network-*; do
		  [ -e "$f" ] || continue
		  network="$(basename "$f")"
		  network="${network#network-}"
		  if [ -n "$compose_filter" ]; then
			[ -f "$state/$network.compose-project" ] || continue
			[ "$(cat "$state/$network.compose-project")" = "$compose_filter" ] || continue
		  fi
		  echo "$network"
		done
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
		rm -f "$state/volume-$name" "$state/compose-volume-$name" "$state/$name.compose-project"
        echo "$name"
        exit 0
        ;;
      ls)
        compose_filter=""
        while [ $# -gt 0 ]; do
          case "$1" in
            -q) shift ;;
            --filter)
              case "$2" in
                label=com.docker.compose.project=*) compose_filter="${2#label=com.docker.compose.project=}" ;;
              esac
              shift 2
              ;;
            *) shift ;;
          esac
        done
        for f in "$state"/compose-volume-*; do
          [ -e "$f" ] || continue
          volume="$(basename "$f")"
          volume="${volume#compose-volume-}"
          if [ -n "$compose_filter" ]; then
            [ -f "$state/$volume.compose-project" ] || continue
            [ "$(cat "$state/$volume.compose-project")" = "$compose_filter" ] || continue
          fi
          echo "$volume"
        done
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
  compose)
    files=""
    project=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -f|--file) files="$files $2"; shift 2 ;;
        -p|--project-name) project="$2"; shift 2 ;;
        --project-directory) shift 2 ;;
        *) break ;;
      esac
    done
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    case "$sub" in
      config)
        if [ "${1:-}" = "--services" ]; then
          printf '%s\n' ${FAKE_DOCKER_COMPOSE_SERVICES:-web}
          exit 0
        fi
		if [ "${1:-}" = "--format" ] && [ "${2:-}" = "json" ]; then
		  if [ -n "${FAKE_DOCKER_COMPOSE_CONFIG_JSON:-}" ]; then
			printf '%s\n' "$FAKE_DOCKER_COMPOSE_CONFIG_JSON"
		  else
			printf '%s\n' '{"services":{"web":{}}}'
		  fi
		  exit 0
		fi
        ;;
      up)
        service=""
        while [ $# -gt 0 ]; do
          case "$1" in
            -d|--detach|--remove-orphans|--build) shift ;;
            --*) shift ;;
            *) service="$1"; shift ;;
          esac
        done
        [ -n "$service" ] || service="web"
        [ -n "$project" ] || project="fake-compose"
        name="compose-${project}-${service}"
        preview=""
        override=""
        for f in $files; do override="$f"; done
        if [ -n "$override" ] && [ -f "$override" ]; then
          preview="$(sed -n 's/.*vivero.preview: *["'\'' ]*\([^"'\'' ]*\).*/\1/p' "$override" | head -n1)"
        fi
        [ -n "$preview" ] && printf '%s' "$preview" > "$state/$name.preview"
        printf '%s' "$project" > "$state/$name.compose-project"
        printf '%s' "$service" > "$state/$name.service"
        printf '%s' "$(pwd)" > "$state/$name.cwd"
        : > "$state/$name.ports"
        if [ -n "$override" ] && [ -f "$override" ]; then
          sed -n 's/.*127\.0\.0\.1::\([0-9][0-9]*\).*/\1/p' "$override" | while IFS= read -r port; do
            [ -n "$port" ] || continue
            printf '%s/tcp|127.0.0.1:%s\n' "$port" "$port" >> "$state/$name.ports"
          done
        fi
        if [ ! -s "$state/$name.ports" ]; then
          printf '3000/tcp|127.0.0.1:3000\n' >> "$state/$name.ports"
        fi
        : > "$state/$name.log"
        (sleep 3600) >/dev/null 2>&1 &
        echo $! > "$state/$name.pid"
		if [ -n "${FAKE_DOCKER_COMPOSE_UP_FAIL:-}" ]; then
		  printf '%s\n' "$FAKE_DOCKER_COMPOSE_UP_FAIL" > "$state/$name.log"
		  echo "$FAKE_DOCKER_COMPOSE_UP_FAIL" >&2
		  exit 7
		fi
        echo "$name"
        exit 0
        ;;
      ps)
        if [ "${1:-}" = "-q" ]; then shift; fi
        service="${1:-web}"
        [ -n "$project" ] || project="fake-compose"
        name="compose-${project}-${service}"
        if [ -f "$state/$name.pid" ]; then echo "$name"; exit 0; fi
        exit 1
        ;;
	  run)
		while [ $# -gt 0 ]; do
		  case "$1" in
			--rm|-T|--no-TTY) shift ;;
			*) break ;;
		  esac
		done
		service="${1:-web}"
		shift || true
		[ -n "$project" ] || project="fake-compose"
		printf 'run' > "$state/compose-${project}-${service}.setup-mode"
		cd "$(pwd)"
		exec "$@"
		;;
	  exec)
		while [ $# -gt 0 ]; do
		  case "$1" in
			-T|--no-TTY) shift ;;
			*) break ;;
		  esac
		done
		service="${1:-web}"
		shift || true
		[ -n "$project" ] || project="fake-compose"
		printf 'exec' > "$state/compose-${project}-${service}.setup-mode"
		name="compose-${project}-${service}"
		cwd="$(cat "$state/$name.cwd" 2>/dev/null || pwd)"
		cd "$cwd"
		exec "$@"
		;;
      down)
        [ -n "$project" ] || project="fake-compose"
        for f in "$state"/*.compose-project; do
          [ -e "$f" ] || continue
          name="$(basename "$f" .compose-project)"
          [ "$(cat "$f")" = "$project" ] || continue
          if [ -f "$state/$name.pid" ]; then kill "$(cat "$state/$name.pid")" 2>/dev/null || true; fi
          rm -f "$state/$name.pid" "$state/$name.exited" "$state/$name.exit-code" "$state/$name.expected-completion" "$state/$name.preview" "$state/$name.service" "$state/$name.cwd" "$state/$name.compose-project"
        done
        exit 0
        ;;
    esac
    echo "unsupported docker compose command $sub" >&2
    exit 2
    ;;
  ps)
    preview_filter=""
    compose_filter=""
	no_trunc=0
    while [ $# -gt 0 ]; do
      case "$1" in
        -a|-q|-aq|-qa) shift ;;
		--no-trunc) no_trunc=1; shift ;;
        --filter)
          case "$2" in
            label=vivero.preview=*) preview_filter="${2#label=vivero.preview=}" ;;
            label=com.docker.compose.project=*) compose_filter="${2#label=com.docker.compose.project=}" ;;
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
      if [ -n "$compose_filter" ]; then
        [ -f "$state/$name.compose-project" ] || continue
        [ "$(cat "$state/$name.compose-project")" = "$compose_filter" ] || continue
      fi
	  rendered="$name"
	  if [ "$no_trunc" -eq 0 ]; then
		case "$name" in
		  *[!0-9a-f]*) ;;
		  *)
			if [ "${#name}" -eq 64 ]; then rendered="$(printf '%s' "$name" | cut -c1-12)"; fi
			;;
		esac
	  fi
	  echo "$rendered"
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
        rm -f "$state/$name.pid" "$state/$name.exited" "$state/$name.exit-code" "$state/$name.expected-completion" "$state/$name.preview" "$state/$name.service" "$state/$name.cwd" "$state/$name.compose-project"
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
