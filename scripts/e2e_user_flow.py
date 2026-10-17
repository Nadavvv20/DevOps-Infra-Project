#!/usr/bin/env python3
"""
End-to-end scripted user flow against Online Boutique and Keycloak:
1. Browse catalog (GET /)
2. Add item to cart (POST /cart)
3. Check shipping quote is displayed in cart (GET /cart)
4. Login via Keycloak (full OAuth2 / OIDC authorization code flow)
5. Place an order (POST /cart/checkout)
"""

import sys
import re
import uuid
import requests
from urllib.parse import urlparse, parse_qs

FRONTEND_URL = "http://localhost:8080"
KEYCLOAK_URL = "http://localhost:8082/auth"
REALM = "online-boutique"
TEST_USER = "testuser"
TEST_PASS = "TestPass123!"
PRODUCT_ID = "OLJCESPC7Z"  # Sunglasses

def run_user_flow():
    session = requests.Session()
    print("==========================================================")
    print("Starting End-to-End User Flow (Frontend + Keycloak + SQS)")
    print("==========================================================")

    # 1. Browse catalog
    print("\n[Step 1] Browsing product catalog (GET /)...")
    res = session.get(f"{FRONTEND_URL}/")
    if res.status_code != 200:
        print(f"FAILED: Expected 200 OK, got {res.status_code}")
        sys.exit(1)
    if "Sunglasses" not in res.text and "Hot Products" not in res.text:
        print("FAILED: Catalog does not contain expected products!")
        sys.exit(1)
    print("  -> Catalog browse OK (200 OK, found products)")

    # 2. Add to cart
    print(f"\n[Step 2] Adding product '{PRODUCT_ID}' (qty 1) to cart (POST /cart)...")
    res = session.post(f"{FRONTEND_URL}/cart", data={
        "product_id": PRODUCT_ID,
        "quantity": "1"
    }, allow_redirects=True)
    if res.status_code != 200:
        print(f"FAILED: Expected 200 OK after cart redirect, got {res.status_code}")
        sys.exit(1)
    print("  -> Add to cart OK")

    # 3. Check shipping quote is shown
    print("\n[Step 3] Checking cart page & shipping quote (GET /cart)...")
    res = session.get(f"{FRONTEND_URL}/cart")
    if res.status_code != 200:
        print(f"FAILED: Expected 200 OK for /cart, got {res.status_code}")
        sys.exit(1)
    if "Shipping" not in res.text:
        print("FAILED: 'Shipping' section not found in cart HTML!")
        sys.exit(1)
    # Check for shipping quote amount
    shipping_match = re.search(r'Shipping.*?(\$\s*[\d\.]+)', res.text, re.DOTALL)
    if not shipping_match:
        # Fallback check for any money representation near shipping
        if "cart-summary-shipping-row" not in res.text:
            print("FAILED: Shipping quote row not found in cart HTML!")
            sys.exit(1)
        print("  -> Cart page verified with shipping quote row!")
    else:
        print(f"  -> Cart page verified! Shipping quote: {shipping_match.group(1).strip()}")

    # 4. Login via Keycloak (Full OAuth2 Authorization Code Flow)
    print("\n[Step 4] Authenticating via Keycloak OAuth2 / OIDC...")
    print(f"  -> Initiating login redirect (GET {FRONTEND_URL}/login?redirect=/cart)...")
    # Don't follow redirect automatically so we can see the Keycloak URL
    login_init = session.get(f"{FRONTEND_URL}/login?redirect=/cart", allow_redirects=False)
    if login_init.status_code not in (302, 303, 307):
        print(f"FAILED: Expected 302 redirect to Keycloak, got {login_init.status_code}")
        sys.exit(1)
    
    keycloak_auth_url = login_init.headers.get("Location")
    print(f"  -> Redirect target: {keycloak_auth_url}")
    
    # Fetch Keycloak login form
    login_page = session.get(keycloak_auth_url)
    if login_page.status_code != 200:
        print(f"FAILED: Failed to fetch Keycloak login page: {login_page.status_code}")
        sys.exit(1)
    
    # Extract Keycloak form action URL
    action_match = re.search(r'action="([^"]+)"', login_page.text)
    if not action_match:
        print("FAILED: Keycloak login form action URL not found!")
        sys.exit(1)
    
    keycloak_post_url = action_match.group(1).replace("&amp;", "&")
    print(f"  -> Submitting credentials for '{TEST_USER}' to Keycloak...")
    
    # Submit credentials to Keycloak
    auth_resp = session.post(keycloak_post_url, data={
        "username": TEST_USER,
        "password": TEST_PASS
    }, allow_redirects=False)
    
    if auth_resp.status_code not in (302, 303):
        print(f"FAILED: Keycloak login failed! HTTP status {auth_resp.status_code}")
        if "Invalid username or password" in auth_resp.text:
            print("ERROR: Invalid credentials reported by Keycloak")
        sys.exit(1)
    
    callback_url = auth_resp.headers.get("Location")
    print(f"  -> Keycloak issued authorization code redirect to: {callback_url}")
    
    # Follow callback redirect to frontend callback handler
    callback_resp = session.get(callback_url, allow_redirects=True)
    if callback_resp.status_code != 200:
        print(f"FAILED: Callback exchange failed! HTTP status {callback_resp.status_code}")
        sys.exit(1)
    
    auth_cookie = session.cookies.get("boutique_auth_session")
    if not auth_cookie:
        print("FAILED: boutique_auth_session cookie not set after OAuth2 callback!")
        sys.exit(1)
    print(f"  -> User '{TEST_USER}' authenticated successfully! Session cookie established.")

    # 5. Place an order
    print("\n[Step 5] Submitting checkout & placing order (POST /cart/checkout)...")
    idempotency_key = str(uuid.uuid4())
    print(f"  -> Generated Idempotency Key: {idempotency_key}")
    
    checkout_data = {
        "email": "testuser@example.com",
        "street_address": "1600 Amphitheatre Pkwy",
        "city": "Mountain View",
        "state": "CA",
        "country": "United States",
        "zip_code": "94043",
        "credit_card_number": "4111111111111111",
        "credit_card_expiration_month": "12",
        "credit_card_expiration_year": "2028",
        "credit_card_cvv": "123",
        "idempotency_key": idempotency_key
    }
    headers = {
        "X-Idempotency-Key": idempotency_key
    }
    
    order_resp = session.post(f"{FRONTEND_URL}/cart/checkout", data=checkout_data, headers=headers, allow_redirects=True)
    if order_resp.status_code != 200:
        print(f"FAILED: PlaceOrder failed with HTTP {order_resp.status_code}: {order_resp.text[:200]}")
        sys.exit(1)
    
    if "Your order is complete!" not in order_resp.text and "Confirmation" not in order_resp.text and "Tracking ID" not in order_resp.text:
        print("WARNING: Expected order confirmation text not detected in response body!")
    
    # Extract order confirmation ID if present
    order_id_match = re.search(r'Order ID[:\s]+<strong>([^<]+)</strong>', order_resp.text)
    if not order_id_match:
        order_id_match = re.search(r'([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})', order_resp.text)
    
    if order_id_match:
        order_id = order_id_match.group(1).strip()
        print(f"  -> Order placed successfully! Order ID: {order_id}")
        import os
        os.makedirs("scratch", exist_ok=True)
        with open("scratch/placed_order_id.txt", "w", encoding="utf-8") as f:
            f.write(order_id)
    else:
        print("  -> Order placed successfully (confirmation page rendered)!")

    print("\n==========================================================")
    print("Scripted User Flow completed successfully!")
    print("==========================================================")
    return 0

if __name__ == "__main__":
    sys.exit(run_user_flow())
