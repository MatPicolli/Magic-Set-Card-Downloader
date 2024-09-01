import os
import asyncio
import logging
from typing import List, Dict, Any
import aiohttp
import aiofiles
import tkinter as tk
from tkinter import filedialog
import PySimpleGUI as sg

# Constants
SET_URL = "https://api.scryfall.com/sets"
CARD_URL = "https://api.scryfall.com/cards/named?fuzzy="
QUALITY_OPTIONS = ["small", "normal", "large"]

# Configure logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

async def download_image(session: aiohttp.ClientSession, url: str, local: str, nome: str, nomeclatura: str, quality: str):
    folder = f"{local}/{nomeclatura.upper()}"
    os.makedirs(folder, exist_ok=True)

    nome = nome.replace(":", "")
    file_path = f"{folder}/{nome}.full.jpg"

    try:
        async with session.get(url) as response:
            if response.status == 200:
                async with aiofiles.open(file_path, "wb") as f:
                    await f.write(await response.read())
                logging.info(f"Downloaded: {nome}")
            else:
                logging.error(f"Failed to download {nome}: HTTP {response.status}")
    except Exception as e:
        logging.error(f"Error downloading {nome}: {str(e)}")

async def set_handler(session: aiohttp.ClientSession, nomeclatura: str, local: str, dados_do_set: List[Dict[str, Any]], quality: str):
    url_do_set_selecionado = next((i["search_uri"] for i in dados_do_set if i["code"] == nomeclatura), None)
    if not url_do_set_selecionado:
        logging.error(f"Set {nomeclatura} not found")
        return

    async with session.get(url_do_set_selecionado) as response:
        if response.status == 200:
            dados_do_set_selecionado = await response.json()
            tasks = []
            for carta in dados_do_set_selecionado["data"]:
                if carta["layout"] == "adventure":
                    nome_carta, _, _ = carta["name"].partition(" //")
                    url_imagem = carta["image_uris"][quality]
                    tasks.append(download_image(session, url_imagem, local, nome_carta, nomeclatura, quality))
                elif "//" in carta["name"] and carta["layout"] != "adventure":
                    for cardface in carta["card_faces"]:
                        nome_carta = cardface["name"]
                        url_imagem = cardface["image_uris"][quality]
                        tasks.append(download_image(session, url_imagem, local, nome_carta, nomeclatura, quality))
                else:
                    nome_carta = carta["name"]
                    url_imagem = carta["image_uris"][quality]
                    tasks.append(download_image(session, url_imagem, local, nome_carta, nomeclatura, quality))
            await asyncio.gather(*tasks)
        else:
            logging.error(f"Failed to fetch set data: HTTP {response.status}")

async def card_handler(session: aiohttp.ClientSession, dados_do_card: Dict[str, Any], local: str, quality: str):
    async with session.get(dados_do_card["prints_search_uri"]) as response:
        if response.status == 200:
            prints_do_card = await response.json()
            tasks = []
            for i in prints_do_card["data"]:
                try:
                    if i["layout"] == "adventure":
                        nome_carta, _, _ = i["name"].partition(" //")
                        url_imagem = i["image_uris"][quality]
                        tasks.append(download_image(session, url_imagem, local, nome_carta, i["set"], quality))
                    elif "//" in i["name"] and i["layout"] != "adventure":
                        for cardface in i["card_faces"]:
                            nome_carta = cardface["name"]
                            url_imagem = cardface["image_uris"][quality]
                            tasks.append(download_image(session, url_imagem, local, nome_carta, i["set"], quality))
                    else:
                        nome_carta = i["name"]
                        url_imagem = i["image_uris"][quality]
                        tasks.append(download_image(session, url_imagem, local, nome_carta, i["set"], quality))
                except Exception as e:
                    logging.error(f"Error processing {i['name']} from {i['set']}: {str(e)}")
            await asyncio.gather(*tasks)
        else:
            logging.error(f"Failed to fetch card prints: HTTP {response.status}")

async def fetch_set_data():
    async with aiohttp.ClientSession() as session:
        async with session.get(SET_URL) as response:
            if response.status == 200:
                dados_sets = await response.json()
                return dados_sets["data"]
            else:
                logging.error(f"Failed to fetch set data: HTTP {response.status}")
                return []

def create_set_window(dados_do_set):
    sg.theme("LightBlue7")
    dados_do_set_nome = [f"{index['name']} ({str(index['code']).upper()})" for index in dados_do_set]

    layout = [
        [sg.Text("Set:"), sg.Combo(dados_do_set_nome, size=(50, 1), key="-SETNOME-")],
        [sg.Text("Set (nomenclature):"), sg.Input(size=(40, 1), key="-SETNOMECLATURA-")],
        [sg.Button("Download Location", size=(15, 1)), sg.Input(key="-CAMINHO_DOWNLOAD-")],
        [sg.Text("Card quality:"), sg.Combo(QUALITY_OPTIONS, size=(15, 1), default_value=QUALITY_OPTIONS[2], key="-QUALIDADE-"),
         sg.Push(), sg.Button("Download Set", size=(15, 1))],
        [sg.Text(key="-TXT-")]
    ]

    return sg.Window("Set downloader", layout)

def create_card_window():
    sg.theme("LightBlue6")

    layout = [
        [sg.Text("Card:"), sg.Input(size=(40, 1), key="-CARDNOME-")],
        [sg.Button("Download Location", size=(15, 1)), sg.Input(key="-CAMINHO_DOWNLOAD-")],
        [sg.Text("Card quality:"), sg.Combo(QUALITY_OPTIONS, size=(15, 1), default_value=QUALITY_OPTIONS[2], key="-QUALIDADE-"),
         sg.Push(), sg.Button("Download Card", size=(15, 1))],
        [sg.Text(key="-TXT-")]
    ]

    return sg.Window("Card downloader", layout)

def create_main_window():
    sg.theme("Dark")

    layout = [
        [sg.Button("SET", size=(30, 1), key="-SET-")],
        [sg.Button("CARD", size=(30, 1), key="-CARD-")]
    ]

    return sg.Window("MTG Card Downloader", layout)

async def main():
    main_window = create_main_window()
    
    while True:
        event, values = main_window.read()
        if event == sg.WINDOW_CLOSED:
            break

        if event == "-SET-":
            dados_do_set = await fetch_set_data()
            set_window = create_set_window(dados_do_set)
            
            while True:
                set_event, set_values = set_window.read()
                if set_event == sg.WINDOW_CLOSED:
                    break

                if set_event == "Download Location":
                    set_window["-CAMINHO_DOWNLOAD-"].update(filedialog.askdirectory())

                if set_event == "Download Set" and set_values["-CAMINHO_DOWNLOAD-"]:
                    nomeclatura = set_values["-SETNOMECLATURA-"].lower()
                    if any(set_data["code"] == nomeclatura for set_data in dados_do_set):
                        set_window["-TXT-"].update("Downloading...")
                        async with aiohttp.ClientSession() as session:
                            await set_handler(session, nomeclatura, set_values["-CAMINHO_DOWNLOAD-"], dados_do_set, set_values["-QUALIDADE-"])
                        set_window["-TXT-"].update("Download completed")
                    else:
                        sg.popup("Set does not exist.")
                elif set_event == "Download Set" and not set_values["-CAMINHO_DOWNLOAD-"]:
                    sg.popup("Please select a download location first!")

            set_window.close()

        if event == "-CARD-":
            card_window = create_card_window()

            while True:
                card_event, card_values = card_window.read()
                if card_event == sg.WINDOW_CLOSED:
                    break

                if card_event == "Download Location":
                    card_window["-CAMINHO_DOWNLOAD-"].update(filedialog.askdirectory())

                if card_event == "Download Card":
                    if not card_values["-CAMINHO_DOWNLOAD-"]:
                        sg.popup("Please select a download location first!")
                    elif not card_values["-CARDNOME-"]:
                        sg.popup("Please enter a card name!")
                    else:
                        card_name = card_values["-CARDNOME-"].lower().replace(" ", "+").replace("/", "+").replace(",", "+").replace("'", "")
                        async with aiohttp.ClientSession() as session:
                            async with session.get(CARD_URL + card_name) as response:
                                if response.status == 200:
                                    dados_do_card = await response.json()
                                    await card_handler(session, dados_do_card, card_values["-CAMINHO_DOWNLOAD-"], card_values["-QUALIDADE-"])
                                    sg.popup("Download completed")
                                else:
                                    sg.popup("Card does not exist.")

            card_window.close()

    main_window.close()

if __name__ == "__main__":
    asyncio.run(main())
