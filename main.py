from tkinter.filedialog import askdirectory
import FreeSimpleGUI as Sg
import requests

set_url = "https://api.scryfall.com/sets"
card_url = "https://api.scryfall.com/cards/named?fuzzy="

def download_imagem(url, local, nome, nomeclatura):
    import os
    if os.path.exists(f"{local}/{nomeclatura.upper()}"):
        pass
    else:
        os.makedirs(f"{local}/{nomeclatura.upper()}")

    # Faz a requisição para a URL da imagem
    response = requests.get(url)

    try:
        # Verifique o status da requisição
        if response.status_code == 200:
            # Requisição bem sucedida
            # Salve a imagem no local especificado
            if ":" in nome:
                nome = nome.replace(":", "")
            with open(f"{local}/{nomeclatura.upper()}/{nome}.full.jpg", "wb") as f:
                f.write(response.content)
        else:
            # Erro na requisição
            print(f"Erro ao baixar imagem: {response.status_code}")
    except ValueError:
        print("Erro ao baixar imagem | ou local inexistente!")


# Faz o download de cada carta do set
def set_handler(nomeclatura, local, dados_do_set, quality, janela):

    url_do_set_selecionado = ""
    for i in dados_do_set:
        if i["code"] == nomeclatura:
            url_do_set_selecionado = i["search_uri"]
            break

    response = requests.get(url_do_set_selecionado)
    if response.status_code == 200:
        # Requisição bem sucedida
        dados_do_set_selecionado = response.json()

        for carta in dados_do_set_selecionado["data"]:
            if carta["layout"] == "adventure":
                nome_carta, _, _ = carta["name"].partition(" //")
                url_imagem = carta["image_uris"][quality]
                download_imagem(url_imagem, local, nome_carta, nomeclatura)

            elif "//" in carta["name"] and carta["layout"] != "adventure":
                for cardface in carta["card_faces"]:
                    nome_carta = cardface["name"]
                    url_imagem = cardface["image_uris"][quality]
                    download_imagem(url_imagem, local, nome_carta, nomeclatura)
            else:
                nome_carta = carta["name"]
                url_imagem = carta["image_uris"][quality]
                download_imagem(url_imagem, local, nome_carta, nomeclatura)


def verifica_se_set_existe(nomeclatura, dados_sets) -> bool:
    value = False
    for index in dados_sets:
        if nomeclatura == index["code"]:
            value = True
            break
    return value


def janela_set():
    response = requests.get(set_url)

    dados_do_set = []
    dados_do_set_nome = []

    if response.status_code == 200:
        # Requisição bem sucedida
        dados_sets = response.json()

        for index in dados_sets["data"]:
            dados_do_set.append(index)
            aux = f"{index['name']} ({str(index['code']).upper()})"
            dados_do_set_nome.append(aux)

    else:
        # Erro na requisição
        print(f"Erro: {response.status_code}")

    quality = ["small", "normal", "large"]

    Sg.theme("LightBlue7")

    layout = [
        [Sg.Text("Set:"), Sg.DropDown(dados_do_set_nome, size=(50, 1), key="-SETNOME-")],
        [Sg.Text("Set (nomeclatura):"), Sg.InputText(size=(40, 1), key="-SETNOMECLATURA-")],
        [Sg.Button("Local de Download", size=(15, 1)), Sg.InputText(key="-CAMINHO_DOWNLOAD-")],
        [Sg.Text("Card quality:"), Sg.DropDown(quality, size=(15, 1), default_value=quality[2], key="-QUALIDADE-"),
         Sg.Push(), Sg.Button("Download Set", size=(15, 1))],
        [Sg.Text(key="-TXT-")]

    ]

    janela = Sg.Window("Set downloader", layout)

    while True:
        evento, valor = janela.read()
        if evento == Sg.WINDOW_CLOSED:
            break

        if evento == "Local de Download":
            janela["-CAMINHO_DOWNLOAD-"].update(askdirectory())

        if evento == "Download Set" and valor["-CAMINHO_DOWNLOAD-"] != "":
            if verifica_se_set_existe(valor["-SETNOMECLATURA-"], dados_do_set):
                janela["-TXT-"].update("Downloading...")
                set_handler(str(valor["-SETNOMECLATURA-"]).lower(),
                            valor["-CAMINHO_DOWNLOAD-"], dados_do_set, valor["-QUALIDADE-"], janela)
                janela["-TXT-"].update("Downloading...Finished")
            else:
                Sg.Popup("Set inexistente.")

        elif evento == "Download Set" and valor["-CAMINHO_DOWNLOAD-"] == "":
            Sg.Popup("É necessário selecionar o caminho de download antes!")

    janela.close()


def card_handler(dados_do_card, local, qualidade):
    response = requests.get(dados_do_card["prints_search_uri"])
    if response.status_code == 200:
        # Requisição bem sucedida
        prints_do_card = response.json()
        for i in prints_do_card["data"]:
            try:
                if i["layout"] == "adventure":
                    nome_carta, _, _ = i["name"].partition(" //")
                    url_imagem = i["image_uris"][qualidade]
                    download_imagem(url_imagem, local, nome_carta, i["set"])

                elif "//" in i["name"] and i["layout"] != "adventure":
                    for cardface in i["card_faces"]:
                        nome_carta = cardface["name"]
                        url_imagem = cardface["image_uris"][qualidade]
                        download_imagem(url_imagem, local, nome_carta, i["set"])
                else:
                    nome_carta = i["name"]
                    url_imagem = i["image_uris"][qualidade]
                    download_imagem(url_imagem, local, nome_carta, i["set"])
            except:
                print(f'error trying to downloading {str(i["name"]).upper()} from {str(i["set"]).upper()}')


def janela_card():
    quality = ["small", "normal", "large"]

    Sg.theme("LightBlue6")

    layout = [
        [Sg.Text("Card:"), Sg.InputText(size=(40, 1), key="-CARDNOME-")],
        [Sg.Button("Local de Download", size=(15, 1)), Sg.InputText(key="-CAMINHO_DOWNLOAD-")],
        [Sg.Text("Card quality:"), Sg.DropDown(quality, size=(15, 1), default_value=quality[2], key="-QUALIDADE-"),
         Sg.Push(), Sg.Button("Download Card", size=(15, 1))],
        [Sg.Text(key="-TXT-")]

    ]

    janela = Sg.Window("Card downloader", layout)

    while True:
        evento, valor = janela.read()
        if evento == Sg.WINDOW_CLOSED:
            break

        if evento == "Local de Download":
            janela["-CAMINHO_DOWNLOAD-"].update(askdirectory())

        if evento == "Download Card" and valor["-CAMINHO_DOWNLOAD-"] == "":
            Sg.Popup("É necessário selecionar o caminho de download antes!")

        elif evento == "Download Card" and valor["-CARDNOME-"] == "":
            Sg.Popup("É necessário escrever o nome da card antes!")

        elif evento == "Download Card" and valor["-CAMINHO_DOWNLOAD-"] != "":
            response = requests.get(card_url + valor["-CARDNOME-"].lower().replace(" ", "+").replace("/", "+").replace(",", "+").replace("'", ""))
            if response.status_code == 200:
                # Requisição bem sucedida
                dados_do_card = response.json()
                card_handler(dados_do_card, valor["-CAMINHO_DOWNLOAD-"], valor["-QUALIDADE-"])
            else:
                # Erro na requisição
                Sg.Popup("Card não existe.")

    janela.close()


# Janela princial do programa
def janela_principal():

    # tema (cores) da janela
    Sg.theme("Dark")

    layout = [
        [Sg.Button("SET", size=(30, 1), key="-SET-")],
        [Sg.Button("CARD", size=(30, 1), key="-CARD-")]
    ]

    janela = Sg.Window("Test", layout)

    while True:
        evento, valore = janela.read()
        if evento == Sg.WINDOW_CLOSED:
            break

        if evento == "-SET-":
            janela_set()

        if evento == "-CARD-":
            janela_card()

    janela.close()


if __name__ == "__main__":
    janela_principal()
