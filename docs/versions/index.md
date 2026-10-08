---
layout: default
title: Versions
nav_order: 17
---

# Versions
{: .no_toc }

Every released version of this specification. Each time the site is published, it gets a new version, and a frozen, read-only copy of it is kept here. A link to an old version keeps showing exactly what that version said.
{: .fs-6 .fw-300 }

This is the version of the **documentation**. It is separate from the API's own `/v1` path version, which is covered in [Conventions](../api-reference/conventions/). For what changed, see the [Changelog](../changelog/).

---

{% assign current = site.data.versions.current -%}
{% if current -%}
| Version | Released | Change |
|---|---|---|
{% assign listed = site.data.versions.archive | unshift: current -%}
{% for v in listed -%}
| [v{{ v.version }}]({{ v.version }}/){% if forloop.first %} **(current)**{% endif %} | {{ v.released }} | {{ v.summary | default: "" | replace: "|", "\|" }}{% if v.commit %} ([`{{ v.commit | slice: 0, 7 }}`](https://github.com/CompositeCode/substratalapps.com/commit/{{ v.commit }})){% endif %} |
{% endfor %}
{% else %}
This is a local build. Version numbers and the archive are assigned only when the site is built and published on GitHub.
{: .note }
{% endif %}
