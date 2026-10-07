# Лабораторная: Docker Compose, KVM и Ansible

Небольшое приложение из двух сервисов:

- **frontend**: nginx отдаёт страницу `index.html` и проксирует `/api/` на бэкенд;
- **backend**: сервис на Go, отдаёт JSON по `GET /api/hello` (поля `message`, `host`, `time`).

Страница при открытии запрашивает `/api/hello` и выводит ответ бэкенда.

## Схема

```
Ноутбук (Windows, браузер)
   │  http://192.168.77.130:8080
   ▼
Ubuntu-VM в VMware Workstation (KVM-хост, nested VT-x, ens33 192.168.77.130)
   │  iptables DNAT 8080 → 192.168.122.227:8080
   ▼
labvm (Ubuntu 24.04 cloud image, KVM/libvirt, 192.168.122.227)
   │  Docker Compose
   ▼
контейнер frontend (nginx :80, опубликован как 8080)
   │  proxy_pass http://backend:5000
   ▼
контейнер backend (Go :5000)
```

Ubuntu-VM нужна только как Linux-хост с KVM: ноутбук работает на Windows, поэтому KVM получен через вложенную виртуализацию в VMware Workstation. Вся «боевая» часть (Docker Compose, приложение) работает внутри `labvm`, созданной через KVM.

## Структура репозитория

```
backend/            исходники Go-бэкенда и Dockerfile
frontend/           index.html, nginx.conf, Dockerfile
compose.yaml        описание двух сервисов
vm/                 cloud-init конфигурация для labvm (user-data, meta-data)
ansible/            inventory.ini, ansible.cfg, playbook.yml
README.md
```

## 1. Запуск через Docker Compose (локально)

```bash
docker compose up -d --build
docker compose ps
```

Адрес: <http://localhost:8080>. Порт опубликован только у фронтенда; бэкенд доступен ему по имени сервиса `backend` внутри сети Compose.

## 2. Проверка связи фронтенда с бэкендом

```bash
curl http://localhost:8080/api/hello
```

Пример ответа:

```json
{"message":"Привет от бэкенда на Go!","host":"0a0d030a2ed5","time":"2026-10-07T15:24:55Z"}
```

Запрос идёт через nginx фронтенда (`/api/` → `backend:5000`), поэтому успешный ответ подтверждает связь. Поле `host` равно id контейнера бэкенда (`docker ps`), так видно, в каком именно окружении отвечает сервис. Страница в браузере показывает ту же строку: «Привет от бэкенда на Go! (контейнер: …, время: …)».

## 3. Проверка виртуализации и KVM

Окружение: ноутбук на Windows, VMware Workstation, гостевая ОС Ubuntu 24.04 LTS (x86_64) с включённой опцией *Virtualize Intel VT-x/EPT or AMD-V/RVI*.

```
$ egrep -c '(vmx|svm)' /proc/cpuinfo
4

$ kvm-ok
INFO: /dev/kvm exists
KVM acceleration can be used

$ ls -l /dev/kvm
crw-rw----+ 1 root kvm 10, 232 окт  7 16:39 /dev/kvm
```

Установка стека:

```bash
sudo apt install -y qemu-kvm libvirt-daemon-system libvirt-clients virtinst cloud-image-utils
sudo usermod -aG libvirt,kvm $USER
```

## 4. Создание VM в KVM (вручную)

Базовый образ и ключ:

```bash
sudo wget https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img \
  -O /var/lib/libvirt/images/base-noble.img
ssh-keygen -t ed25519 -N "" -f ~/.ssh/id_ed25519
```

Конфигурация cloud-init лежит в `vm/user-data` и `vm/meta-data` (пользователь `ubuntu`, вход по SSH-ключу, hostname `labvm`).

```bash
cd vm
cloud-localds seed.iso user-data meta-data

sudo qemu-img create -f qcow2 -F qcow2 \
  -b /var/lib/libvirt/images/base-noble.img \
  /var/lib/libvirt/images/labvm.qcow2 20G
sudo cp seed.iso /var/lib/libvirt/images/labvm-seed.iso

sudo virt-install \
  --name labvm \
  --memory 2048 --vcpus 2 \
  --disk /var/lib/libvirt/images/labvm.qcow2,format=qcow2 \
  --disk /var/lib/libvirt/images/labvm-seed.iso,device=cdrom \
  --os-variant ubuntu24.04 \
  --network network=default \
  --import --noautoconsole
```

Результат:

```
$ sudo virsh list --all
 Id   Name    State
 1    labvm   running

$ sudo virsh domifaddr labvm
 Name     MAC address         Protocol   Address
 vnet0    52:54:00:54:c8:98   ipv4       192.168.122.227/24

$ ssh ubuntu@192.168.122.227
ubuntu@labvm:~$
```

## 5. Доступ к приложению с ноутбука

Адрес `192.168.122.227` виден только внутри Ubuntu-VM (сеть libvirt `default`), поэтому порт пробрасывается на Ubuntu-VM:

```bash
sudo sysctl -w net.ipv4.ip_forward=1
sudo iptables -t nat -A PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 192.168.122.227:8080
sudo iptables -t nat -A POSTROUTING -d 192.168.122.227 -p tcp --dport 8080 -j MASQUERADE
sudo iptables -I FORWARD -d 192.168.122.227 -p tcp --dport 8080 -j ACCEPT
sudo iptables -I FORWARD -s 192.168.122.227 -p tcp --sport 8080 -j ACCEPT
```

Чтобы правила переживали перезагрузку:

```bash
sudo apt install -y iptables-persistent
sudo netfilter-persistent save
```

Проверка:

- с ноутбука (Windows): <http://192.168.77.130:8080> или `curl http://192.168.77.130:8080/api/hello`;
- с Ubuntu-VM напрямую в labvm: `curl http://192.168.122.227:8080/api/hello`.

Чтобы убедиться, что ответ идёт именно из labvm, а не из локального Compose на Ubuntu-VM, локальный проект остановлен (`docker compose down`), а `host` в ответе сверяется с `docker ps` внутри labvm.

## 6. Ansible

Ansible запускается на Ubuntu-VM и управляет `labvm` по SSH.

Файлы: `ansible/inventory.ini`, `ansible/ansible.cfg`, `ansible/playbook.yml`.

Подготовка:

```bash
sudo apt install -y ansible
ansible-galaxy collection install community.docker
cd ansible
ansible labvm -m ping
```

Запуск:

```bash
cd ansible
ansible-playbook playbook.yml
```

Playbook:

1. ставит `docker.io`, `docker-compose-v2`, `python3-docker` (модуль `apt`);
2. включает и запускает сервис Docker, добавляет `ubuntu` в группу `docker`;
3. создаёт `/opt/lab`;
4. копирует `compose.yaml`, `backend/` и `frontend/` (модуль `copy`);
5. запускает проект через `community.docker.docker_compose_v2`; образы пересобираются, только если исходники изменились.

Используются только штатные модули без `shell`/`command`, поэтому повторные запуски не меняют состояние.

### Первый запуск

```
PLAY RECAP
192.168.122.227 : ok=8  changed=4  unreachable=0  failed=0  skipped=0  rescued=0  ignored=0
```

### Повторный запуск (идемпотентность)

```
TASK [Copy backend and frontend sources]
ok: [192.168.122.227] => (item=backend)
ok: [192.168.122.227] => (item=frontend)
TASK [Start Compose project]
ok: [192.168.122.227]

PLAY RECAP
192.168.122.227 : ok=8  changed=0  unreachable=0  failed=0  skipped=0  rescued=0  ignored=0
```

После повторного запуска приложение продолжает отвечать, id контейнера не меняется:

```
$ curl http://192.168.122.227:8080/api/hello
{"message":"Привет от бэкенда на Go!","host":"0a0d030a2ed5","time":"2026-10-07T15:24:55Z"}
```

Предупреждения `Cannot parse event from line` и про `buildx` при запуске модуля Compose безвредны: модуль не разбирает часть вывода `docker compose`, на результат это не влияет.

