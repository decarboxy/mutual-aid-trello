# Mutual Aid Trello tool

This is a tool used for collecting data on recipients from the Academic Mutual Aid (https://academicmutualaid.org/) trello board.

If you aren't involved in the administration of AMA, this is not useful to you. It's a very specific tool with a 
global user base of perhaps 6 people. 

## Usage

You'll need a trello API key and token. To get that, go here: https://trello.com/app-key and follow the instructions
to manually generate the token.  Protect both the API key and token like you would a username and password. 

Then you can run the command like this:

```
./mutual-aid-trello csv --api-key <api-key> --token <token> --out output.csv
```