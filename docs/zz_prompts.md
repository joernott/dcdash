# Prompt Log

All prompts given to the AI assistant during development of dcdash, in chronological order.
Each entry records the date and the sprint it belongs to.

---

## 2026-11-12 — Sprint 0 (Planning)

dcdash is a new project to create a unified dashboard to manage central services in a data center.
I want you to review it's concept and create an implementation plan for it. That plan contains sprints and each sprint consists of multiple tasks. Compleded tasks will be marked with a checkmark in the implementation plan. All of my prompts should be stored in a file docs/zz_prompts.md and each sprint should be developed in it's own branch, using one or more commits per task in a sprint. After completing a sprint, it's branch will be squash merged into main and the next sprint branch will be forked from main. The sprint branch will remain for documentation purpose. If you need to add a task during implementation, it will be added as subtask to that task,  egg. Task 1 in Sprint 1 has the number 1.1, if you find out, that another task is needed to finish 1.1, a subtask 1.1.1 will be added. The implementation plan will be amended.

---

## 2026-11-12 — Sprint 0 (Planning)

2.7 VM and container creation must be part of v0.1.1
2.10 Use zerolog for logging

Answers to your questions:
Q1: Yes
Q2: Configurable per role
Q3: Yes, it is needed
Q4: Zone creation and deletion is needed
Q5: Yes. Additional hooks can be installed as well, if needed
Q6: Yes
Q7: English and German for now
Q8: Yes

One additional feature I almost missed, that is always requested by users is the support for a dark mode

---

## 2026-11-12 — Sprint 0 (Planning)

DCDash will copy zone files to the primary DNS server via SFTP. We can't guarantee its installation on the primary BIND server

---

## 2026-11-12 — Sprint 0 (Planning)

yes, please go ahead

---

## 2026-11-12 — Sprint 1 (Foundation)

I set up a dedicated development vm for you: dev.vm.test.sf.thse.net where you have a user "claude" with password "[REDACTED]" and sudo rights, so you can install software as needed. You can use ssh using the ssh key /c/Users/ext-se-jo/.ssh/id_ed25519_claude to connect to it. You can install whatever software you need on that. I've also added that key as a deploy key with write access to github.

---

## 2026-11-12 — Sprint 1 (Foundation)

GPL-3.0-or-later is fine with me. There will be no need to install samba as we have a samba server available in the network. I also can scale up the VM as needed.

---

## 2026-11-12 — Sprint 1 (Foundation)

Please squas-merge sprint 1 into main, push theit and start with sprint 2

---

## 2026-11-12 — Sprint 2 (TLS)

Please continue with the next sprint. You can use the ldap bind user "ssp-service@windows.test.sf.thse.net" with password "[REDACTED]" to connect to the domain controller "ldaps://DC1.windows.test.sf.thse.net:636"
