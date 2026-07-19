// ==UserScript==
// @name         Qobuz Downloader Injector
// @namespace    http://tampermonkey.net/
// @version      2.1
// @description  Injects download buttons into Qobuz album pages and lists
// @author       Gemini CLI
// @match        *://*.qobuz.com/*
// @grant        GM_xmlhttpRequest
// @grant        GM_addStyle
// @connect      localhost
// @updateURL    https://raw.githubusercontent.com/JulienMaille/qobuz-dl/main/qobuz-downloader.user.js
// @downloadURL  https://raw.githubusercontent.com/JulienMaille/qobuz-dl/main/qobuz-downloader.user.js
// ==/UserScript==

(function() {
    'use strict';

    console.log('[QobuzDL] Script v2.1 active');

    const SERVER_URL = 'http://localhost:8687/api/download';

    GM_addStyle(`
        .qobuz-dl-btn {
            margin-left: 5px;
            padding: 4px 6px;
            background-color: #2563eb;
            color: white;
            border: 1px solid white;
            border-radius: 6px;
            cursor: pointer;
            font-size: 10px;
            font-weight: bold;
            vertical-align: middle;
            display: inline-block;
            line-height: 1;
            transition: background-color 0.2s;
            z-index: 100;
        }
        .qobuz-dl-btn:hover {
            background-color: #1d4ed8;
        }
        .qobuz-dl-btn:disabled {
            opacity: 0.7;
            cursor: not-allowed;
        }
        .qobuz-dl-btn.success {
            background-color: #22c55e !important;
        }
        .qobuz-dl-btn.error {
            background-color: #ef4444 !important;
        }

        /* Layout fix for chart lists */
        .album-container {
            position: relative;
        }
        .album-container .qobuz-dl-btn {
            position: absolute;
            right: 10px;
            top: 10%;
            transform: translateY(-50%);
            margin: 0;
        }

        /* Specific fix for product items lists */
        .product__items .product__container {
            position: relative;
            padding-right: 40px !important;
        }
        .product__items .product__container .qobuz-dl-btn {
            position: absolute;
            right: 0;
            top: 10px;
            margin: 0;
            font-size: 14px !important;
            padding: 8px 16px !important;
        }

        /* Large button for album page */
        .qobuz-dl-btn-large {
            font-size: 14px !important;
            padding: 8px 16px !important;
            display: block !important;
            margin-top: 10px !important;
            width: fit-content;
        }

        /* Editorial / Magazine styling */
        .magazine-story__content .qobuz-dl-btn,
        .editorial-content .qobuz-dl-btn {
            margin-left: 10px;
        }
    `);

    // Function to trigger download
    function triggerDownload(id, button) {
        const url = `${SERVER_URL}?id=${id}`;
        const originalText = button.textContent;
        button.textContent = '...';
        button.disabled = true;

        GM_xmlhttpRequest({
            method: "GET",
            url: url,
            onload: function(response) {
                if (response.status === 200) {
                    button.textContent = 'OK';
                    button.classList.add('success');
                } else {
                    button.textContent = 'Err';
                    button.classList.add('error');
                }
                setTimeout(() => {
                    button.textContent = originalText;
                    button.classList.remove('success', 'error');
                    button.disabled = false;
                }, 2000);
            },
            onerror: function() {
                button.textContent = 'Fail';
                button.classList.add('error');
                setTimeout(() => {
                    button.textContent = originalText;
                    button.classList.remove('success', 'error');
                    button.disabled = false;
                }, 2000);
            }
        });
    }

    // Function to create a button
    function createDownloadButton(id, isLarge = false) {
        const btn = document.createElement('button');
        btn.textContent = 'DL';
        btn.className = 'qobuz-dl-btn';
        if (isLarge) btn.classList.add('qobuz-dl-btn-large');

        btn.onclick = function(e) {
            e.preventDefault();
            e.stopPropagation();
            triggerDownload(id, btn);
        };
        return btn;
    }

    function extractId(text) {
        if (!text) return null;
        const parts = text.split('/').filter(p => p);
        const last = parts[parts.length - 1];
        if (last && /^[a-zA-Z0-9]+$/.test(last) && last.length >= 5) {
            return last.split('?')[0];
        }
        return null;
    }

    function run() {
        // 1. Scan <a> tags for album IDs
        const links = document.querySelectorAll('a[href*="/album/"]');
        for (let link of links) {
            if (link.dataset.dlInjected) continue;

            const href = link.getAttribute('href');
            const id = extractId(href);
            if (!id) continue;

            // Find common container to prevent duplicates
            const container = link.closest('li') ||
                  link.closest('.product__item') ||
                  link.closest('.album-item') ||
                  link.closest('.product-item') ||
                  link.closest('.album-container') ||
                  link.closest('.magazine-story__content') ||
                  link.closest('.editorial-content') ||
                  link.parentElement;

            if (container.dataset.dlInjectedId === id) {
                link.dataset.dlInjected = "true";
                continue;
            }

            // AVOID injecting on covers
            if (link.classList.contains('album-cover') ||
                link.classList.contains('product__cover') ||
                link.querySelector('img') ||
                link.closest('.product__cover') ||
                link.closest('.album-cover')) {
                link.dataset.dlInjected = "true";
                continue;
            }

            const btn = createDownloadButton(id);

            // SPECIAL CASE: if we are inside a .product__items list, inject into .product__container
            const itemsList = link.closest('.product__items');
            const productContainer = container.querySelector('.product__container');

            if (itemsList && productContainer) {
                productContainer.appendChild(btn);
            } else {
                // DEFAULT: Append to a good metadata sub-container or the parent link
                let target = container.querySelector('.album-item__metadata') || 
                    container.querySelector('.release-item__metadata') ||
                    container.querySelector('.text') ||
                    container.querySelector('.magazine-story__text') ||
                    container.querySelector('.editorial-content__text');

                if (!target) {
                    if (container.classList.contains('album-item__metadata') ||
                        container.classList.contains('release-item__metadata') ||
                        container.classList.contains('text')) {
                        target = container;
                        target.appendChild(btn);
                    } else {
                        // FALLBACK for editorial/magazine: inject right after the link
                        link.after(btn);
                    }
                } else {
                    target.appendChild(btn);
                }
            }

            link.dataset.dlInjected = "true";
            container.dataset.dlInjectedId = id;
        }

        // 2. Main album page specific injection
        if (window.location.pathname.includes('/album/')) {
            const pathParts = window.location.pathname.split('/').filter(p => p);
            const id = pathParts[pathParts.length - 1];

            const header = document.querySelector('.album-meta__infos') ||
                  document.querySelector('.release-meta__infos') ||
                  document.querySelector('h1');

            if (header && !header.dataset.dlInjected && id && id.length >= 5) {
                const btn = createDownloadButton(id, true);
                btn.textContent = 'Download Album';
                header.appendChild(btn);
                header.dataset.dlInjected = "true";
            }
        }
    }

    const observer = new MutationObserver(() => run());
    observer.observe(document.body, { childList: true, subtree: true });

    run();
})();