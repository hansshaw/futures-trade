These HTML fixtures preserve only the relevant visible list, title, publication
and content nodes from ordinary unauthenticated HTTP GETs on 2026-10-28.
They contain factual prices and their original structure, not a mock market.

- list.html: https://www.boyar.cn/column/7.html
- prices.html: https://www.boyar.cn/article/1269531.html
- arrivals.html: https://www.boyar.cn/article/1269532.html
- notice-future.html: https://www.boyar.cn/article/1269541.html
- notice-effective.html: https://www.boyar.cn/article/1269319.html

The price table does not specify a unit. Do not annotate it as yuan/tonne.
The arrivals sentence does not specify whether it counts newly arrived trucks or
trucks remaining at the gate. Do not annotate it as a known unloading queue.
The later notice is effective 2026-10-29 07:00 Asia/Shanghai; a prior collection
must record only its announcement. Tests never fetch these URLs.
